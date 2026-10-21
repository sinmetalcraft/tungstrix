package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"time"

	"cloud.google.com/go/civil"
	"cloud.google.com/go/spanner"
)

// QueryParameterを含むQueryは、値が渡されていなくてもSpannerが文脈から型を推論して実行計画を作れることが多い。
// ただしUNNEST(@shardids)のように文脈から要素型を決められない場合はINT64と仮定されて失敗するため、
// -paramsで型を指定してNULLをbindできるようにしている。

// queryParams is -paramsで渡すJSONの構造
// keyはtext_fingerprint、valueはQueryParameter名とその型/値
// 順位(CPUTOP09など)は実行するたびに変わるため、SQLの内容から決まるtext_fingerprintをkeyにしている
type queryParams map[string]map[string]paramSpec

// paramSpec is QueryParameter1つに対する指定
// Valueを省略した場合はTypeのNULLをbindする
// 実行計画を取得するだけなら値は不要なので、通常はTypeだけ書けばよい
type paramSpec struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value,omitempty"`
}

func loadQueryParams(path string) (queryParams, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read params file %s: %w", path, err)
	}
	var params queryParams
	if err := json.Unmarshal(b, &params); err != nil {
		return nil, fmt.Errorf("failed to parse params file %s: %w", path, err)
	}
	// keyの打ち間違いは黙って無視されると気付けないので、読み込み時に検証する
	for fingerprint := range params {
		if _, err := strconv.ParseInt(fingerprint, 10, 64); err != nil {
			return nil, fmt.Errorf("invalid text_fingerprint %q in %s: text_fingerprintは10進数の整数です", fingerprint, path)
		}
	}
	return params, nil
}

// bindFor is text_fingerprintに対応するQueryParameterをspanner.Statementにbindできる形で返す
func (p queryParams) bindFor(textFingerprint int64) (map[string]any, error) {
	specs, ok := p[strconv.FormatInt(textFingerprint, 10)]
	if !ok {
		return nil, nil
	}

	bound := make(map[string]any, len(specs))
	for name, spec := range specs {
		typeSpec, ok := paramTypeSpecs[spec.Type]
		if !ok {
			return nil, fmt.Errorf("param %q: 型 %q は指定できません。指定可能な型: %v", name, spec.Type, supportedParamTypes())
		}
		if len(spec.Value) == 0 {
			bound[name] = typeSpec.nullValue
			continue
		}
		v, err := typeSpec.parseValue(spec.Value)
		if err != nil {
			return nil, fmt.Errorf("param %q: valueを型 %s として読めませんでした: %w", name, spec.Type, err)
		}
		bound[name] = v
	}
	return bound, nil
}

// paramTypeSpec is JSONに書かれた型名1つ分のbind方法
type paramTypeSpec struct {
	// nullValue is valueが省略されたときにbindする、その型のNULL
	nullValue any
	// parseValue is JSONのvalueをbindできるGoの値に変換する
	parseValue func(raw json.RawMessage) (any, error)
}

func newParamTypeSpec[T any](nullValue any) paramTypeSpec {
	return paramTypeSpec{
		nullValue: nullValue,
		parseValue: func(raw json.RawMessage) (any, error) {
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			return v, nil
		},
	}
}

// civil.Dateはencoding/jsonが使うjson.Unmarshalerを実装していないため、文字列として読んでからParseDateする
func parseDate(raw json.RawMessage) (any, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return civil.ParseDate(s)
}

func parseDateArray(raw json.RawMessage) (any, error) {
	var ss []string
	if err := json.Unmarshal(raw, &ss); err != nil {
		return nil, err
	}
	dates := make([]civil.Date, 0, len(ss))
	for _, s := range ss {
		d, err := civil.ParseDate(s)
		if err != nil {
			return nil, err
		}
		dates = append(dates, d)
	}
	return dates, nil
}

var paramTypeSpecs = map[string]paramTypeSpec{
	"BOOL":      newParamTypeSpec[bool](spanner.NullBool{}),
	"INT64":     newParamTypeSpec[int64](spanner.NullInt64{}),
	"FLOAT64":   newParamTypeSpec[float64](spanner.NullFloat64{}),
	"STRING":    newParamTypeSpec[string](spanner.NullString{}),
	"BYTES":     newParamTypeSpec[[]byte]([]byte(nil)),
	"TIMESTAMP": newParamTypeSpec[time.Time](spanner.NullTime{}),
	"DATE":      {nullValue: spanner.NullDate{}, parseValue: parseDate},

	"ARRAY<BOOL>":      newParamTypeSpec[[]bool]([]bool(nil)),
	"ARRAY<INT64>":     newParamTypeSpec[[]int64]([]int64(nil)),
	"ARRAY<FLOAT64>":   newParamTypeSpec[[]float64]([]float64(nil)),
	"ARRAY<STRING>":    newParamTypeSpec[[]string]([]string(nil)),
	"ARRAY<BYTES>":     newParamTypeSpec[[][]byte]([][]byte(nil)),
	"ARRAY<TIMESTAMP>": newParamTypeSpec[[]time.Time]([]time.Time(nil)),
	"ARRAY<DATE>":      {nullValue: []civil.Date(nil), parseValue: parseDateArray},
}

func supportedParamTypes() []string {
	types := make([]string, 0, len(paramTypeSpecs))
	for t := range paramTypeSpecs {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

var (
	// Spannerの文字列リテラルにメールアドレスなどが入っているとQueryParameterに見えてしまうため、先に取り除く
	stringLiteralRe = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	// @{FORCE_INDEX=...}のようなhintは`{`から始まるのでmatchしない
	queryParamRe = regexp.MustCompile(`@([A-Za-z_][A-Za-z0-9_]*)`)
)

// extractQueryParamNames is SQLに含まれるQueryParameter名を出現順に重複なしで返す
func extractQueryParamNames(text string) []string {
	stripped := stringLiteralRe.ReplaceAllString(text, "''")

	var names []string
	seen := map[string]bool{}
	for _, m := range queryParamRe.FindAllStringSubmatch(stripped, -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// buildParamsTemplate is 実行計画の取得に失敗したQueryの分だけ、-paramsに渡すJSONの雛形を作る
func buildParamsTemplate(results []*queryResult) ([]byte, error) {
	template := queryParams{}
	for _, r := range results {
		if r.PlanErr == nil || len(r.ParamNames) == 0 {
			continue
		}
		specs := make(map[string]paramSpec, len(r.ParamNames))
		for _, name := range r.ParamNames {
			// 型が空のままだとbind時にエラーになるので、埋め忘れに気付ける
			specs[name] = paramSpec{Type: ""}
		}
		template[strconv.FormatInt(r.Stats.TextFingerprint, 10)] = specs
	}
	if len(template) == 0 {
		return nil, nil
	}
	return json.MarshalIndent(template, "", "  ")
}

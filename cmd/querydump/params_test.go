package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	tungstrixspanner "github.com/sinmetalcraft/tungstrix/tools/spanner"
)

func newStats(textFingerprint int64) *tungstrixspanner.QueryStats {
	return &tungstrixspanner.QueryStats{TextFingerprint: textFingerprint}
}

func TestExtractQueryParamNames(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "出現順に重複なしで返す",
			text: "SELECT Id FROM UNNEST(@shardids) AS s WHERE Namespace = @namespace AND Status IN UNNEST(@statuses) LIMIT @limit",
			want: []string{"shardids", "namespace", "statuses", "limit"},
		},
		{
			name: "同じparamが複数回出てきても1つ",
			text: "SELECT Id FROM T WHERE A = @limit OR B = @limit",
			want: []string{"limit"},
		},
		{
			name: "FORCE_INDEXのhintはQueryParameterではない",
			text: "SELECT Id FROM T@{FORCE_INDEX=TByA} WHERE A = @a",
			want: []string{"a"},
		},
		{
			name: "文字列リテラルの中のアットマークは拾わない",
			text: "SELECT Id FROM T WHERE Mail = 'foo@example.com' AND A = @a",
			want: []string{"a"},
		},
		{
			name: "paramなし",
			text: "SELECT 1",
			want: nil,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := extractQueryParamNames(tt.text)
			if !slices.Equal(got, tt.want) {
				t.Errorf("extractQueryParamNames() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryParamsBindFor(t *testing.T) {
	const fingerprint = 6950124233627194671

	params := queryParams{
		"6950124233627194671": {
			// valueを省略した場合はその型のNULLをbindする
			"shardids": {Type: "ARRAY<INT64>"},
			"statuses": {Type: "ARRAY<STRING>"},
			// valueを指定した場合はその値をbindする
			"limit": {Type: "INT64", Value: json.RawMessage("100")},
			"to":    {Type: "TIMESTAMP", Value: json.RawMessage(`"2026-09-01T00:00:00Z"`)},
		},
	}

	got, err := params.bindFor(fingerprint)
	if err != nil {
		t.Fatalf("bindFor() returned err: %v", err)
	}

	if v, ok := got["shardids"].([]int64); !ok || v != nil {
		t.Errorf("shardids = %#v, want []int64(nil)", got["shardids"])
	}
	if v, ok := got["statuses"].([]string); !ok || v != nil {
		t.Errorf("statuses = %#v, want []string(nil)", got["statuses"])
	}
	if got["limit"] != int64(100) {
		t.Errorf("limit = %#v, want int64(100)", got["limit"])
	}
	wantTo := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if to, ok := got["to"].(time.Time); !ok || !to.Equal(wantTo) {
		t.Errorf("to = %#v, want %v", got["to"], wantTo)
	}

	// 指定のないtext_fingerprintはnilを返し、Spannerの型推論に任せる
	got, err = params.bindFor(1)
	if err != nil {
		t.Fatalf("bindFor() returned err: %v", err)
	}
	if got != nil {
		t.Errorf("bindFor(1) = %#v, want nil", got)
	}
}

func TestQueryParamsBindForNullTypes(t *testing.T) {
	// 型名を書いただけでbindできることを全ての型で確認する
	for _, typeName := range supportedParamTypes() {
		params := queryParams{"1": {"p": {Type: typeName}}}
		got, err := params.bindFor(1)
		if err != nil {
			t.Errorf("type %s: bindFor() returned err: %v", typeName, err)
			continue
		}
		if _, ok := got["p"]; !ok {
			t.Errorf("type %s: p is not bound", typeName)
		}
	}

	// NULLをbindできる型はspanner.Statementが受け取れる必要がある
	stmt := spanner.NewStatement("SELECT @p")
	for _, typeName := range supportedParamTypes() {
		params := queryParams{"1": {"p": {Type: typeName}}}
		bound, err := params.bindFor(1)
		if err != nil {
			t.Fatalf("type %s: %v", typeName, err)
		}
		stmt.Params["p"] = bound["p"]
	}
}

func TestQueryParamsBindForInvalidType(t *testing.T) {
	params := queryParams{"1": {"p": {Type: "INT"}}}
	if _, err := params.bindFor(1); err == nil {
		t.Error("bindFor() should return err for unknown type")
	}

	// 雛形のtypeを埋め忘れた場合も気付けるようにエラーにする
	params = queryParams{"1": {"p": {Type: ""}}}
	if _, err := params.bindFor(1); err == nil {
		t.Error("bindFor() should return err for empty type")
	}
}

func TestLoadQueryParamsInvalidFingerprint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "params.json")
	if err := os.WriteFile(path, []byte(`{"CPUTOP09": {"p": {"type": "INT64"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadQueryParams(path); err == nil {
		t.Error("loadQueryParams() should return err when key is not a text_fingerprint")
	}
}

func TestBuildParamsTemplate(t *testing.T) {
	results := []*queryResult{
		// 成功したQueryは雛形に含めない
		{BaseName: "CPUTOP01", Stats: newStats(1), ParamNames: []string{"a"}},
		// QueryParameterを含まないQuery(DMLなど)も含めない
		{BaseName: "CPUTOP02", Stats: newStats(2), PlanErr: errForTest{}},
		{BaseName: "CPUTOP03", Stats: newStats(3), ParamNames: []string{"shardids"}, PlanErr: errForTest{}},
	}

	b, err := buildParamsTemplate(results)
	if err != nil {
		t.Fatalf("buildParamsTemplate() returned err: %v", err)
	}

	var got queryParams
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("failed to parse template: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("template has %d entries, want 1: %s", len(got), b)
	}
	if got["3"]["shardids"].Type != "" {
		t.Errorf("template type = %q, want empty", got["3"]["shardids"].Type)
	}

	// 失敗したQueryがなければ雛形は出力しない
	b, err = buildParamsTemplate([]*queryResult{{BaseName: "CPUTOP01", Stats: newStats(1)}})
	if err != nil {
		t.Fatalf("buildParamsTemplate() returned err: %v", err)
	}
	if b != nil {
		t.Errorf("buildParamsTemplate() = %s, want nil", b)
	}
}

type errForTest struct{}

func (errForTest) Error() string { return "failed" }

package spanner

import (
	"fmt"
	"regexp"
	"strings"
)

// QueryStatsFilter is ListQueryStats, ListReadStats, ListLockStatsで取得する統計を絞り込む条件
// 種類を増やす場合はこのファイルに実装を足し、ParseQueryStatsFilterにkeyを追加する
// 統計のtableごとに列が異なるため、それぞれのWHERE句に足す条件を返すmethodを持つ
type QueryStatsFilter interface {
	// buildCondition is spanner_sys.query_stats_top_hourのWHERE句に足す条件と、そこで使うQueryParameterを返す
	// paramNameは他のFilterと衝突しないように呼び出し側が決める
	buildCondition(paramName string) (sql string, params map[string]any)
	// buildReadStatsCondition is spanner_sys.read_stats_top_hourのWHERE句に足す条件と、そこで使うQueryParameterを返す
	buildReadStatsCondition(paramName string) (sql string, params map[string]any)
	// buildLockStatsCondition is spanner_sys.lock_stats_top_hourのWHERE句に足す条件と、そこで使うQueryParameterを返す
	buildLockStatsCondition(paramName string) (sql string, params map[string]any)
	// String is ログや出力に使う表記
	String() string
}

// QueryStatsFilterKeys is ParseQueryStatsFilterで指定できるkeyの一覧
var QueryStatsFilterKeys = []string{"table"}

// ParseQueryStatsFilter is "table=WorkflowExecutions" のような key=value をQueryStatsFilterに変換する
func ParseQueryStatsFilter(v string) (QueryStatsFilter, error) {
	key, value, ok := strings.Cut(v, "=")
	if !ok {
		return nil, fmt.Errorf("invalid filter %q: key=value の形式で指定してください。指定できるkey %v", v, QueryStatsFilterKeys)
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("invalid filter %q: valueが空です", v)
	}

	switch key {
	case "table":
		return TableFilter{Table: value}, nil
	default:
		return nil, fmt.Errorf("invalid filter key %q: 指定できるkey %v", key, QueryStatsFilterKeys)
	}
}

// TableFilter is 指定したTableを使っているQueryに絞り込む
// Query Statisticsに残っているのはSQLのtextだけなので、text内に識別子として現れるかで判定している
// そのためCommentや文字列リテラルにTable名が入っているだけのQueryもmatchする
type TableFilter struct {
	Table string
}

func (f TableFilter) buildCondition(paramName string) (string, map[string]any) {
	// Spannerの識別子は大文字小文字を区別しないため(?i)を付ける
	// \bで囲むことで、Table名が他の識別子の一部になっているだけのQueryを除く
	pattern := `(?i)\b` + regexp.QuoteMeta(f.Table) + `\b`
	return fmt.Sprintf("REGEXP_CONTAINS(text, @%s)", paramName), map[string]any{paramName: pattern}
}

func (f TableFilter) String() string {
	return "table=" + f.Table
}

// filtersPlaceholder is SQLのWHERE句にFilterの条件を挿し込む位置
// SQLのコメントなので、置き換えなくてもSQLとしてそのまま実行できる
const filtersPlaceholder = "/*FILTERS*/"

// applyQueryStatsFilters is SQLのplaceholderをFilterの条件で置き換え、bindするQueryParameterを返す
func applyQueryStatsFilters(sql string, filters []QueryStatsFilter) (string, map[string]any, error) {
	return applyFilters(sql, filters, QueryStatsFilter.buildCondition)
}

// applyFilters is SQLのplaceholderを、buildで作ったFilterの条件で置き換える
// 統計のtableによってFilterの条件が異なるため、条件の作り方を引数で受け取る
func applyFilters(sql string, filters []QueryStatsFilter, build func(f QueryStatsFilter, paramName string) (string, map[string]any)) (string, map[string]any, error) {
	if !strings.Contains(sql, filtersPlaceholder) {
		return "", nil, fmt.Errorf("sql does not contain %s", filtersPlaceholder)
	}
	if len(filters) == 0 {
		return strings.Replace(sql, filtersPlaceholder, "", 1), nil, nil
	}

	conditions := make([]string, 0, len(filters))
	params := map[string]any{}
	for i, f := range filters {
		// Table名などをSQLに直接埋め込まないようにQueryParameterを使う
		condition, filterParams := build(f, fmt.Sprintf("filter%d", i))
		conditions = append(conditions, "AND "+condition)
		for name, v := range filterParams {
			params[name] = v
		}
	}
	return strings.Replace(sql, filtersPlaceholder, strings.Join(conditions, "\n  "), 1), params, nil
}

// FormatQueryStatsFilters is ログや出力に使うFilterの表記を返す
func FormatQueryStatsFilters(filters []QueryStatsFilter) string {
	if len(filters) == 0 {
		return "(none)"
	}
	ss := make([]string, 0, len(filters))
	for _, f := range filters {
		ss = append(ss, f.String())
	}
	return strings.Join(ss, " ")
}

func (f TableFilter) buildReadStatsCondition(paramName string) (string, map[string]any) {
	// read_columnsは "UserBalanceAdditions.CreatedAt" のように "Table名.Column名" の形式で入っている
	return fmt.Sprintf("EXISTS (SELECT 1 FROM UNNEST(read_columns) AS c WHERE STARTS_WITH(LOWER(c), LOWER(@%s) || '.'))", paramName),
		map[string]any{paramName: f.Table}
}

func (f TableFilter) buildLockStatsCondition(paramName string) (string, map[string]any) {
	// row_range_start_keyは "userbalanceconsumptions(2b360e4b-...)" のように小文字のTable名とKeyの形式で入っている
	// sample_lock_requestsのcolumnは "UserBalanceConsumptions.PartnerId" のように "Table名.Column名" の形式
	// Keyの書式が変わっても絞り込めるように、どちらかにmatchすれば対象にする
	// SELECT句でsample_lock_requestsを返しているため、WHERE句でそのSTRUCTのfieldを参照すると
	// "Accessing fields of a struct value also present in the final result is not supported" になる
	// そのため同じ内容が入っているsample_lock_requests_json_stringを文字列として探している
	return fmt.Sprintf(`(STARTS_WITH(LOWER(SAFE_CONVERT_BYTES_TO_STRING(row_range_start_key)), LOWER(@%[1]s) || '(') OR STRPOS(LOWER(sample_lock_requests_json_string), '"column":"' || LOWER(@%[1]s) || '.') > 0)`, paramName),
		map[string]any{paramName: f.Table}
}

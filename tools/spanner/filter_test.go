package spanner

import (
	"strings"
	"testing"
)

func TestParseQueryStatsFilter(t *testing.T) {
	cases := []struct {
		name    string
		v       string
		want    string
		wantErr bool
	}{
		{name: "table", v: "table=WorkflowExecutions", want: "table=WorkflowExecutions"},
		{name: "前後の空白は無視する", v: " table = WorkflowExecutions ", want: "table=WorkflowExecutions"},
		{name: "keyがない", v: "WorkflowExecutions", wantErr: true},
		{name: "valueが空", v: "table=", wantErr: true},
		{name: "未知のkey", v: "index=TByA", wantErr: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseQueryStatsFilter(tt.v)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseQueryStatsFilter(%q) should return err", tt.v)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseQueryStatsFilter(%q) returned err: %v", tt.v, err)
			}
			if got.String() != tt.want {
				t.Errorf("String() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestApplyQueryStatsFilters(t *testing.T) {
	// Filterを指定しない場合はplaceholderが消えるだけ
	sql, params, err := applyQueryStatsFilters(queryStatsTotalCPUSQL, nil)
	if err != nil {
		t.Fatalf("applyQueryStatsFilters() returned err: %v", err)
	}
	if strings.Contains(sql, filtersPlaceholder) {
		t.Error("placeholder is not replaced")
	}
	if len(params) != 0 {
		t.Errorf("params = %v, want empty", params)
	}

	// 複数のFilterはANDで結合し、それぞれ別のQueryParameterを使う
	sql, params, err = applyQueryStatsFilters(queryStatsTotalCPUSQL, []QueryStatsFilter{
		TableFilter{Table: "WorkflowExecutions"},
		TableFilter{Table: "ActivityExecutions"},
	})
	if err != nil {
		t.Fatalf("applyQueryStatsFilters() returned err: %v", err)
	}
	if strings.Contains(sql, filtersPlaceholder) {
		t.Error("placeholder is not replaced")
	}
	if !strings.Contains(sql, "AND REGEXP_CONTAINS(text, @filter0)") {
		t.Errorf("filter0 condition is missing:\n%s", sql)
	}
	if !strings.Contains(sql, "AND REGEXP_CONTAINS(text, @filter1)") {
		t.Errorf("filter1 condition is missing:\n%s", sql)
	}
	if params["filter0"] != `(?i)\bWorkflowExecutions\b` {
		t.Errorf("filter0 = %v", params["filter0"])
	}
	if params["filter1"] != `(?i)\bActivityExecutions\b` {
		t.Errorf("filter1 = %v", params["filter1"])
	}

	// placeholderを消してしまった場合に気付けるようにエラーにする
	if _, _, err := applyQueryStatsFilters("SELECT 1", nil); err == nil {
		t.Error("applyQueryStatsFilters() should return err when sql has no placeholder")
	}
}

func TestTableFilterQuotesMeta(t *testing.T) {
	// Table名に正規表現のメタ文字が入っていてもそのまま解釈されないようにする
	sql, params := TableFilter{Table: "A.B"}.buildCondition("filter0")
	if sql != "REGEXP_CONTAINS(text, @filter0)" {
		t.Errorf("sql = %q", sql)
	}
	if params["filter0"] != `(?i)\bA\.B\b` {
		t.Errorf("filter0 = %v, want escaped", params["filter0"])
	}
}

func TestFormatQueryStatsFilters(t *testing.T) {
	if got := FormatQueryStatsFilters(nil); got != "(none)" {
		t.Errorf("FormatQueryStatsFilters(nil) = %q, want (none)", got)
	}
	got := FormatQueryStatsFilters([]QueryStatsFilter{
		TableFilter{Table: "A"},
		TableFilter{Table: "B"},
	})
	if got != "table=A table=B" {
		t.Errorf("FormatQueryStatsFilters() = %q", got)
	}
}

func TestQueryStatsSQLHasFiltersPlaceholder(t *testing.T) {
	// 全てのorderのSQLにplaceholderが必要なので、SQLを追加したときに気付けるようにする
	for _, order := range QueryStatsOrders {
		sql, err := order.sql()
		if err != nil {
			t.Errorf("order %s: %v", order, err)
			continue
		}
		if !strings.Contains(sql, filtersPlaceholder) {
			t.Errorf("order %s: sql does not contain %s", order, filtersPlaceholder)
		}
	}
}

func TestApplyFiltersForReadAndLockStats(t *testing.T) {
	filters := []QueryStatsFilter{TableFilter{Table: "UserBalanceAdditions"}}

	sql, params, err := applyFilters(readStatsTotalCPUSQL, filters, QueryStatsFilter.buildReadStatsCondition)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	if !strings.Contains(sql, "UNNEST(read_columns)") || params["filter0"] != "UserBalanceAdditions" {
		t.Errorf("read stats: sql=%s params=%v", sql, params)
	}

	sql, params, err = applyFilters(lockStatsTotalLockWaitSQL, filters, QueryStatsFilter.buildLockStatsCondition)
	if err != nil {
		t.Fatalf("lock stats: %v", err)
	}
	if !strings.Contains(sql, "row_range_start_key") || !strings.Contains(sql, "sample_lock_requests_json_string") || params["filter0"] != "UserBalanceAdditions" {
		t.Errorf("lock stats: sql=%s params=%v", sql, params)
	}
}

func TestLockStatsUniqueSampleLockRequests(t *testing.T) {
	a := &LockRequest{Column: "T.A", LockMode: "ReaderShared"}
	b := &LockRequest{Column: "T.B", LockMode: "WriterShared"}
	s := &LockStats{SampleLockRequests: []*LockRequest{a, b, {Column: "T.A", LockMode: "ReaderShared"}, nil}}
	got := s.UniqueSampleLockRequests()
	if len(got) != 2 || *got[0] != *a || *got[1] != *b {
		t.Errorf("UniqueSampleLockRequests() = %+v", got)
	}
}

func TestLockStatsRowRangeStartKeyString(t *testing.T) {
	s := &LockStats{RowRangeStartKey: []byte("accounts(1)")}
	if got := s.RowRangeStartKeyString(); got != "accounts(1)" {
		t.Errorf("RowRangeStartKeyString() = %q", got)
	}
	s = &LockStats{RowRangeStartKey: []byte{0xff, 0x01}}
	if got := s.RowRangeStartKeyString(); got != "0xff01" {
		t.Errorf("RowRangeStartKeyString() = %q", got)
	}
}

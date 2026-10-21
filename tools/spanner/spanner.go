package spanner

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"cloud.google.com/go/spanner"
	"github.com/apstndb/spannerplan/plantree/reference"
	"google.golang.org/adk/tool"
	"google.golang.org/api/iterator"
)

//go:embed dmls/query_stats_total_cpu.sql
var queryStatsTotalCPUSQL string

//go:embed dmls/query_stats_avg_latency.sql
var queryStatsAvgLatencySQL string

// QueryStatsOrder is ListQueryStatsで取得するQueryの並び順
type QueryStatsOrder string

const (
	// QueryStatsOrderTotalCPU is 合計CPU使用量(avg_cpu_seconds * execution_countの総和)の降順
	QueryStatsOrderTotalCPU QueryStatsOrder = "cpu"
	// QueryStatsOrderAvgLatency is 平均レイテンシ(execution_countによる加重平均)の降順
	QueryStatsOrderAvgLatency QueryStatsOrder = "latency"
)

// QueryStatsOrders is 指定可能なQueryStatsOrderの一覧
var QueryStatsOrders = []QueryStatsOrder{QueryStatsOrderTotalCPU, QueryStatsOrderAvgLatency}

// ParseQueryStatsOrder is 文字列をQueryStatsOrderに変換する
func ParseQueryStatsOrder(v string) (QueryStatsOrder, error) {
	for _, order := range QueryStatsOrders {
		if QueryStatsOrder(v) == order {
			return order, nil
		}
	}
	return "", fmt.Errorf("invalid order %q", v)
}

// Label is ファイル名やレポートの見出しに使う表記を返す
func (o QueryStatsOrder) Label() string {
	switch o {
	case QueryStatsOrderTotalCPU:
		return "CPUTOP"
	case QueryStatsOrderAvgLatency:
		return "LATENCYTOP"
	default:
		return ""
	}
}

// sql is 並び順に対応するSQLを返す
func (o QueryStatsOrder) sql() (string, error) {
	switch o {
	case QueryStatsOrderTotalCPU:
		return queryStatsTotalCPUSQL, nil
	case QueryStatsOrderAvgLatency:
		return queryStatsAvgLatencySQL, nil
	default:
		return "", fmt.Errorf("invalid order %q", o)
	}
}

type AnalyzeQueryToolParams struct {
	ProjectID  string `json:"projectID" jsonschema:"Spanner ProjectID"`
	InstanceID string `json:"instanceID" jsonschema:"Spanner InstanceID"`
	DatabaseID string `json:"databaseID" jsonschema:"Spanner DatabaseID"`
	SQL        string `json:"sql" jsonschema:"Spanner SQL"`
}

type AnalyzeQueryResult struct {
	AnalyzeQueryResult string `json:"analyzeQueryResult" jsonschema:"Analyze Query Result"`

	// Error
	Err error `json:"err" jsonschema:"Error message, if any"`
}

// AnalyzeQuery is return QueryPlan
func AnalyzeQuery(ctx tool.Context, params AnalyzeQueryToolParams) AnalyzeQueryResult {
	cli, err := spanner.NewClient(ctx, fmt.Sprintf("projects/%s/instances/%s/databases/%s", params.ProjectID, params.InstanceID, params.DatabaseID))
	if err != nil {
		return AnalyzeQueryResult{Err: err}
	}
	defer cli.Close()

	s, err := NewStatisticsService(ctx, cli)
	if err != nil {
		return AnalyzeQueryResult{Err: err}
	}
	v, err := s.AnalyzeQueryRenderTree(ctx, params.SQL)
	if err != nil {
		return AnalyzeQueryResult{Err: err}
	}
	return AnalyzeQueryResult{AnalyzeQueryResult: v}
}

type ListTotalCPUTop10ToolParams struct {
	ProjectID  string `json:"projectID" jsonschema:"Spanner ProjectID"`
	InstanceID string `json:"instanceID" jsonschema:"Spanner InstanceID"`
	DatabaseID string `json:"databaseID" jsonschema:"Spanner DatabaseID"`
}

type ListTotalCPUTop10Result struct {
	Queries []*QueryStats `json:"queries" jsonschema:"Top 10 queries by total CPU usage across the full retention of spanner_sys.query_stats_top_hour, grouped by TEXT_FINGERPRINT"`

	// Error
	Err error `json:"err" jsonschema:"Error message, if any"`
}

// ListTotalCPUTop10 is spanner_sys.query_stats_top_hourの全期間で合計CPU使用量が大きいクエリをTop10取得する
func ListTotalCPUTop10(ctx tool.Context, params ListTotalCPUTop10ToolParams) ListTotalCPUTop10Result {
	cli, err := spanner.NewClient(ctx, fmt.Sprintf("projects/%s/instances/%s/databases/%s", params.ProjectID, params.InstanceID, params.DatabaseID))
	if err != nil {
		return ListTotalCPUTop10Result{Err: err}
	}
	defer cli.Close()

	s, err := NewStatisticsService(ctx, cli)
	if err != nil {
		return ListTotalCPUTop10Result{Err: err}
	}
	v, err := s.ListTotalCPUTop10(ctx)
	if err != nil {
		return ListTotalCPUTop10Result{Err: err}
	}
	return ListTotalCPUTop10Result{Queries: v}
}

type StatisticsService struct {
	cli *spanner.Client
}

func NewStatisticsService(ctx context.Context, cli *spanner.Client) (*StatisticsService, error) {
	return &StatisticsService{cli}, nil
}

func (s *StatisticsService) Close() {
	s.cli.Close()
}

// AnalyzeQueryRenderTree is Query Planをテキスト形式で出力したものを返す
// example
/*
+----+---------------------------------------------------------------------------------------------------+
| ID | Operator                                                                                          |
+----+---------------------------------------------------------------------------------------------------+
| *0 | Distributed Union (distribution_table: Event, execution_method: Row, split_ranges_aligned: false) |
|  1 | +- Local Distributed Union (execution_method: Row)                                                |
|  2 |    +- Serialize Result (execution_method: Row)                                                    |
|  3 |       +- Filter Scan (execution_method: Row, seekable_key_size: 0)                                |
| *4 |          +- Table Scan (Table: Event, execution_method: Row, scan_method: Row)                    |
+----+---------------------------------------------------------------------------------------------------+
Predicates(identified by ID):
 0: Split Range: ($EventID = @event)
 4: Seek Condition: ($EventID = @event)
*/
func (s *StatisticsService) AnalyzeQueryRenderTree(ctx context.Context, sql string) (string, error) {
	return s.AnalyzeQueryRenderTreeWithParams(ctx, sql, nil)
}

// AnalyzeQueryRenderTreeWithParams is QueryParameterをbindしてQuery Planをテキスト形式で出力したものを返す
// Spannerは値が渡されていないQueryParameterの型を文脈から推論するが、UNNEST(@ids)のように
// 推論できない場合はINT64と仮定して失敗するため、その場合に型を指定したNULLなどをbindする
func (s *StatisticsService) AnalyzeQueryRenderTreeWithParams(ctx context.Context, sql string, params map[string]any) (string, error) {
	stmt := spanner.NewStatement(sql)
	for name, v := range params {
		stmt.Params[name] = v
	}

	plan, err := s.cli.Single().AnalyzeQuery(ctx, stmt)
	if err != nil {
		return "", err
	}

	v, err := reference.RenderTreeTable(plan.PlanNodes, reference.RenderModeAuto, reference.FormatTraditional, 0)
	if err != nil {
		return "", err
	}

	return v, nil
}

// QueryStats is spanner_sys.query_stats_top_hourをTEXT_FINGERPRINTで集約した統計
// 並び順に関わらず同じ内容を返すため、ListQueryStatsの全てのQueryStatsOrderで共通に利用する
type QueryStats struct {
	TextFingerprint int64   `spanner:"text_fingerprint"`
	Text            string  `spanner:"text"`
	TextTruncated   bool    `spanner:"text_truncated"`
	RequestTag      string  `spanner:"request_tag"`
	Count           int64   `spanner:"count"`
	AvgLatency      float64 `spanner:"avg_latency"`
	AvgCPU          float64 `spanner:"avg_cpu"`
	TotalCPU        float64 `spanner:"total_cpu"`
	AvgRowsScanned  float64 `spanner:"avg_rows_scanned"`
	AvgRowsReturned float64 `spanner:"avg_rows_returned"`
	AvgBytes        float64 `spanner:"avg_bytes"`
}

// ListTotalCPUTop10 is spanner_sys.query_stats_top_hourの全期間で合計CPU使用量が大きいクエリをTop10取得する
func (s *StatisticsService) ListTotalCPUTop10(ctx context.Context) ([]*QueryStats, error) {
	return s.ListQueryStats(ctx, QueryStatsOrderTotalCPU, 10)
}

// ListAvgLatencyTop25 is spanner_sys.query_stats_top_hourの全期間で平均レイテンシが高いクエリをTop25取得する
func (s *StatisticsService) ListAvgLatencyTop25(ctx context.Context) ([]*QueryStats, error) {
	return s.ListQueryStats(ctx, QueryStatsOrderAvgLatency, 25)
}

// ListQueryStats is spanner_sys.query_stats_top_hourの全期間の統計をorderの降順でlimit件取得する
// TEXT_FINGERPRINTで集約し、avg_*はexecution_countによる加重平均で算出する
// filtersを渡すとWHERE句に条件が追加され、複数渡した場合はANDで結合される
func (s *StatisticsService) ListQueryStats(ctx context.Context, order QueryStatsOrder, limit int64, filters ...QueryStatsFilter) ([]*QueryStats, error) {
	sql, err := order.sql()
	if err != nil {
		return nil, err
	}
	sql, filterParams, err := applyQueryStatsFilters(sql, filters)
	if err != nil {
		return nil, err
	}

	stmt := spanner.NewStatement(sql)
	stmt.Params["limit"] = limit
	for name, v := range filterParams {
		stmt.Params[name] = v
	}

	iter := s.cli.Single().Query(ctx, stmt)
	defer iter.Stop()

	var result []*QueryStats
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var v QueryStats
		if err := row.ToStruct(&v); err != nil {
			return nil, err
		}
		result = append(result, &v)
	}
	return result, nil
}

type ListAvgLatencyTop25ToolParams struct {
	ProjectID  string `json:"projectID" jsonschema:"Spanner ProjectID"`
	InstanceID string `json:"instanceID" jsonschema:"Spanner InstanceID"`
	DatabaseID string `json:"databaseID" jsonschema:"Spanner DatabaseID"`
}

type ListAvgLatencyTop25Result struct {
	Queries []*QueryStats `json:"queries" jsonschema:"Top 25 queries by weighted average latency across the full retention of spanner_sys.query_stats_top_hour, grouped by TEXT_FINGERPRINT"`

	// Error
	Err error `json:"err" jsonschema:"Error message, if any"`
}

// ListAvgLatencyTop25 is spanner_sys.query_stats_top_hourの全期間で平均レイテンシが高いクエリをTop25取得する
func ListAvgLatencyTop25(ctx tool.Context, params ListAvgLatencyTop25ToolParams) ListAvgLatencyTop25Result {
	cli, err := spanner.NewClient(ctx, fmt.Sprintf("projects/%s/instances/%s/databases/%s", params.ProjectID, params.InstanceID, params.DatabaseID))
	if err != nil {
		return ListAvgLatencyTop25Result{Err: err}
	}
	defer cli.Close()

	s, err := NewStatisticsService(ctx, cli)
	if err != nil {
		return ListAvgLatencyTop25Result{Err: err}
	}
	v, err := s.ListAvgLatencyTop25(ctx)
	if err != nil {
		return ListAvgLatencyTop25Result{Err: err}
	}
	return ListAvgLatencyTop25Result{Queries: v}
}

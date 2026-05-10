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

//go:embed dmls/query_status_top_hour_total_cpu_top10.sql
var queryStatusTopHourTotalCPUTop10SQL string

//go:embed dmls/query_stats_avg_latency_top25.sql
var queryStatsAvgLatencyTop25SQL string

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

type ListTopHourTotalCPUTop10ToolParams struct {
	ProjectID  string `json:"projectID" jsonschema:"Spanner ProjectID"`
	InstanceID string `json:"instanceID" jsonschema:"Spanner InstanceID"`
	DatabaseID string `json:"databaseID" jsonschema:"Spanner DatabaseID"`
}

type ListTopHourTotalCPUTop10Result struct {
	Queries []*QueryStatsTopHourTotalCPUTop10 `json:"queries" jsonschema:"Top 10 queries by total CPU usage in the last hour"`

	// Error
	Err error `json:"err" jsonschema:"Error message, if any"`
}

// ListTopHourTotalCPUTop10 is 直近1hでCPU利用率が高いものを10件取得する
func ListTopHourTotalCPUTop10(ctx tool.Context, params ListTopHourTotalCPUTop10ToolParams) ListTopHourTotalCPUTop10Result {
	cli, err := spanner.NewClient(ctx, fmt.Sprintf("projects/%s/instances/%s/databases/%s", params.ProjectID, params.InstanceID, params.DatabaseID))
	if err != nil {
		return ListTopHourTotalCPUTop10Result{Err: err}
	}
	defer cli.Close()

	s, err := NewStatisticsService(ctx, cli)
	if err != nil {
		return ListTopHourTotalCPUTop10Result{Err: err}
	}
	v, err := s.ListTopHourTotalCPUTop10(ctx)
	if err != nil {
		return ListTopHourTotalCPUTop10Result{Err: err}
	}
	return ListTopHourTotalCPUTop10Result{Queries: v}
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
	plan, err := s.cli.Single().AnalyzeQuery(ctx, spanner.NewStatement(sql))
	if err != nil {
		return "", err
	}

	v, err := reference.RenderTreeTable(plan.PlanNodes, reference.RenderModeAuto, reference.FormatTraditional, 0)
	if err != nil {
		return "", err
	}

	return v, nil
}

type QueryStatsTopHourTotalCPUTop10 struct {
	Text       string  `spanner:"text"`
	RequestTag string  `spanner:"request_tag"`
	Count      int64   `spanner:"count"`
	Latency    float64 `spanner:"latency"`
	CPU        float64 `spanner:"cpu"`
	TotalCPU   float64 `spanner:"total_cpu"`
}

// ListTopHourTotalCPUTop10 is 直近1hでCPU利用率が高いものを10件取得する
func (s *StatisticsService) ListTopHourTotalCPUTop10(ctx context.Context) ([]*QueryStatsTopHourTotalCPUTop10, error) {
	iter := s.cli.Single().Query(ctx, spanner.NewStatement(queryStatusTopHourTotalCPUTop10SQL))
	defer iter.Stop()

	var result []*QueryStatsTopHourTotalCPUTop10
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var v QueryStatsTopHourTotalCPUTop10
		if err := row.ToStruct(&v); err != nil {
			return nil, err
		}
		result = append(result, &v)
	}
	return result, nil
}

type QueryStatsAvgLatencyTop25 struct {
	TextFingerprint int64   `spanner:"text_fingerprint"`
	Text            string  `spanner:"text"`
	RequestTag      string  `spanner:"request_tag"`
	Count           int64   `spanner:"count"`
	AvgLatency      float64 `spanner:"avg_latency"`
	AvgCPU          float64 `spanner:"avg_cpu"`
}

type ListAvgLatencyTop25ToolParams struct {
	ProjectID  string `json:"projectID" jsonschema:"Spanner ProjectID"`
	InstanceID string `json:"instanceID" jsonschema:"Spanner InstanceID"`
	DatabaseID string `json:"databaseID" jsonschema:"Spanner DatabaseID"`
}

type ListAvgLatencyTop25Result struct {
	Queries []*QueryStatsAvgLatencyTop25 `json:"queries" jsonschema:"Top 25 queries by weighted average latency across the full retention of spanner_sys.query_stats_top_hour, grouped by TEXT_FINGERPRINT"`

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

// ListAvgLatencyTop25 is spanner_sys.query_stats_top_hourの全期間で平均レイテンシが高いクエリをTop25取得する
// TEXT_FINGERPRINTで集約し、execution_countによる加重平均でavg_latencyを算出する
func (s *StatisticsService) ListAvgLatencyTop25(ctx context.Context) ([]*QueryStatsAvgLatencyTop25, error) {
	iter := s.cli.Single().Query(ctx, spanner.NewStatement(queryStatsAvgLatencyTop25SQL))
	defer iter.Stop()

	var result []*QueryStatsAvgLatencyTop25
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var v QueryStatsAvgLatencyTop25
		if err := row.ToStruct(&v); err != nil {
			return nil, err
		}
		result = append(result, &v)
	}
	return result, nil
}

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

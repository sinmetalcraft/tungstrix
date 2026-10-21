package spanner

import (
	"context"
	_ "embed"
	"errors"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
)

//go:embed dmls/read_stats_total_cpu.sql
var readStatsTotalCPUSQL string

// ReadStats is spanner_sys.read_stats_top_hourをFPRINTで集約した統計
// Read APIで読んだものが対象で、SQLのQueryはQueryStatsに入る
type ReadStats struct {
	Fprint      int64    `spanner:"fprint"`
	ReadColumns []string `spanner:"read_columns"`
	ReadType    string   `spanner:"read_type"`
	RequestTag  string   `spanner:"request_tag"`
	Count       int64    `spanner:"count"`
	// RWCount is Read Write Transactionの中で実行された回数
	RWCount               int64   `spanner:"rw_count"`
	TotalCPU              float64 `spanner:"total_cpu"`
	AvgCPU                float64 `spanner:"avg_cpu"`
	AvgRows               float64 `spanner:"avg_rows"`
	AvgBytes              float64 `spanner:"avg_bytes"`
	AvgLockingDelay       float64 `spanner:"avg_locking_delay"`
	AvgClientWait         float64 `spanner:"avg_client_wait"`
	AvgLeaderRefreshDelay float64 `spanner:"avg_leader_refresh_delay"`
	AvgDiskIOCost         float64 `spanner:"avg_disk_io_cost"`
}

// ListReadStats is spanner_sys.read_stats_top_hourの全期間で合計CPU使用量が大きいReadをlimit件取得する
// FPRINTで集約し、avg_*はexecution_countによる加重平均で算出する
// filtersを渡すとWHERE句に条件が追加され、複数渡した場合はANDで結合される
func (s *StatisticsService) ListReadStats(ctx context.Context, limit int64, filters ...QueryStatsFilter) ([]*ReadStats, error) {
	sql, filterParams, err := applyFilters(readStatsTotalCPUSQL, filters, QueryStatsFilter.buildReadStatsCondition)
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

	var result []*ReadStats
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var v ReadStats
		if err := row.ToStruct(&v); err != nil {
			return nil, err
		}
		result = append(result, &v)
	}
	return result, nil
}

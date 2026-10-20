package spanner

import (
	"context"
	_ "embed"
	"encoding/hex"
	"errors"
	"time"
	"unicode/utf8"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
)

//go:embed dmls/lock_stats_total_lock_wait.sql
var lockStatsTotalLockWaitSQL string

// LockStats is spanner_sys.lock_stats_top_hourをrow_range_start_keyで集約した統計
type LockStats struct {
	RowRangeStartKey []byte  `spanner:"row_range_start_key"`
	TotalLockWait    float64 `spanner:"total_lock_wait"`
	// IntervalCount is このRowがlock_stats_top_hourに現れたintervalの数
	IntervalCount    int64     `spanner:"interval_count"`
	FirstIntervalEnd time.Time `spanner:"first_interval_end"`
	LastIntervalEnd  time.Time `spanner:"last_interval_end"`
	// SampleLockRequests is 全intervalのsampleを連結したもの。重複が含まれるのでUniqueSampleLockRequestsを使う
	SampleLockRequests []*LockRequest `spanner:"sample_lock_requests"`
}

// LockRequest is Lockの競合が起きたときにsampleとして記録されたLockの要求
type LockRequest struct {
	Column         string `spanner:"COLUMN"`
	LockMode       string `spanner:"LOCK_MODE"`
	TransactionTag string `spanner:"TRANSACTION_TAG"`
}

// RowRangeStartKeyString is row_range_start_keyを表示できる文字列にして返す
// 通常は "userbalanceconsumptions(2b360e4b-...)" のような文字列だが、そうでない場合はhexにする
func (s *LockStats) RowRangeStartKeyString() string {
	if utf8.Valid(s.RowRangeStartKey) {
		return string(s.RowRangeStartKey)
	}
	return "0x" + hex.EncodeToString(s.RowRangeStartKey)
}

// UniqueSampleLockRequests is SampleLockRequestsから重複を除いたものを、最初に現れた順で返す
func (s *LockStats) UniqueSampleLockRequests() []*LockRequest {
	seen := map[LockRequest]bool{}
	var result []*LockRequest
	for _, r := range s.SampleLockRequests {
		if r == nil || seen[*r] {
			continue
		}
		seen[*r] = true
		result = append(result, r)
	}
	return result
}

// ListLockStats is spanner_sys.lock_stats_top_hourの全期間でLockの待ち時間の合計が大きいRowをlimit件取得する
// filtersを渡すとWHERE句に条件が追加され、複数渡した場合はANDで結合される
func (s *StatisticsService) ListLockStats(ctx context.Context, limit int64, filters ...QueryStatsFilter) ([]*LockStats, error) {
	sql, filterParams, err := applyFilters(lockStatsTotalLockWaitSQL, filters, QueryStatsFilter.buildLockStatsCondition)
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

	var result []*LockStats
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var v LockStats
		if err := row.ToStruct(&v); err != nil {
			return nil, err
		}
		result = append(result, &v)
	}
	return result, nil
}

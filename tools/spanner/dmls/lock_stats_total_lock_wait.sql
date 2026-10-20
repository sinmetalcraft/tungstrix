# spanner_sys.lock_stats_top_hour の保持期間全体で、Lockの待ち時間の合計が大きいRowを@limit件取得する
# 同じRow(row_range_start_key)が複数の interval にまたがるためGROUP BYで集約している
# sample_lock_requestsは interval ごとのsampleを全て連結しているので、重複は呼び出し側で除く
SELECT row_range_start_key,
       SUM(lock_wait_seconds) AS total_lock_wait,
       COUNT(*) AS interval_count,
       MIN(interval_end) AS first_interval_end,
       MAX(interval_end) AS last_interval_end,
       ARRAY_CONCAT_AGG(sample_lock_requests) AS sample_lock_requests
FROM spanner_sys.lock_stats_top_hour
WHERE TRUE
  /*FILTERS*/
GROUP BY row_range_start_key
ORDER BY total_lock_wait DESC
LIMIT @limit;

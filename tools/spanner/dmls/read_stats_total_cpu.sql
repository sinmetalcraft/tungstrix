# spanner_sys.read_stats_top_hour の保持期間全体で合計CPU使用量が大きいReadを@limit件取得する
# Read APIで読んだものが対象で、SQLのQueryはquery_stats_top_hourに入る
# 同じRead(FPRINT)が複数の interval にまたがるためGROUP BYで集約している
# total_cpuは interval ごとの avg_cpu_seconds * execution_count の総和、avg_*はexecution_countによる加重平均
SELECT fprint,
       ANY_VALUE(read_columns) AS read_columns,
       IFNULL(ANY_VALUE(read_type), '') AS read_type,
       IFNULL(ANY_VALUE(request_tag), '') AS request_tag,
       SUM(execution_count) AS count,
       IFNULL(SUM(run_in_rw_transaction_execution_count), 0) AS rw_count,
       IFNULL(SUM(avg_cpu_seconds * execution_count), 0) AS total_cpu,
       IFNULL(SAFE_DIVIDE(SUM(avg_cpu_seconds * execution_count), SUM(execution_count)), 0) AS avg_cpu,
       IFNULL(SAFE_DIVIDE(SUM(avg_rows * execution_count), SUM(execution_count)), 0) AS avg_rows,
       IFNULL(SAFE_DIVIDE(SUM(avg_bytes * execution_count), SUM(execution_count)), 0) AS avg_bytes,
       IFNULL(SAFE_DIVIDE(SUM(avg_locking_delay_seconds * execution_count), SUM(execution_count)), 0) AS avg_locking_delay,
       IFNULL(SAFE_DIVIDE(SUM(avg_client_wait_seconds * execution_count), SUM(execution_count)), 0) AS avg_client_wait,
       IFNULL(SAFE_DIVIDE(SUM(avg_leader_refresh_delay_seconds * execution_count), SUM(execution_count)), 0) AS avg_leader_refresh_delay,
       IFNULL(SAFE_DIVIDE(SUM(avg_disk_io_cost * execution_count), SUM(execution_count)), 0) AS avg_disk_io_cost
FROM spanner_sys.read_stats_top_hour
WHERE TRUE
  /*FILTERS*/
GROUP BY fprint
ORDER BY total_cpu DESC
LIMIT @limit;

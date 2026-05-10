# 直前の 1 時間における CPU 使用率が最も高いクエリ
SELECT text,
       request_tag,
       execution_count AS count,
       avg_latency_seconds AS latency,
       avg_cpu_seconds AS cpu,
       execution_count * avg_cpu_seconds AS total_cpu
FROM spanner_sys.query_stats_top_hour
WHERE interval_end =
    (SELECT MAX(interval_end)
    FROM spanner_sys.query_stats_top_hour)
ORDER BY total_cpu DESC
LIMIT 10;
# spanner_sys.query_stats_top_hour の保持期間全体で平均レイテンシが高いクエリTop25
# 同じクエリ(TEXT_FINGERPRINT)が複数の interval にまたがるためGROUP BYで集約し、
# execution_count による加重平均でavg_latencyを算出している
SELECT text_fingerprint,
       ANY_VALUE(text) AS text,
       ANY_VALUE(request_tag) AS request_tag,
       SUM(execution_count) AS count,
       SAFE_DIVIDE(SUM(avg_latency_seconds * execution_count), SUM(execution_count)) AS avg_latency,
       SAFE_DIVIDE(SUM(avg_cpu_seconds * execution_count), SUM(execution_count)) AS avg_cpu
FROM spanner_sys.query_stats_top_hour
GROUP BY text_fingerprint
ORDER BY avg_latency DESC
LIMIT 25;

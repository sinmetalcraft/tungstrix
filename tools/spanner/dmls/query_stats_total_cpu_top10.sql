# spanner_sys.query_stats_top_hour の保持期間全体で合計CPU使用量が大きいクエリTop10
# 同じクエリ(TEXT_FINGERPRINT)が複数の interval にまたがるためGROUP BYで集約し、
# total_cpuは interval ごとの execution_count * avg_cpu_seconds の総和で算出している
SELECT text_fingerprint,
       ANY_VALUE(text) AS text,
       ANY_VALUE(request_tag) AS request_tag,
       SUM(execution_count) AS count,
       SAFE_DIVIDE(SUM(avg_latency_seconds * execution_count), SUM(execution_count)) AS avg_latency,
       SAFE_DIVIDE(SUM(avg_cpu_seconds * execution_count), SUM(execution_count)) AS avg_cpu,
       SUM(avg_cpu_seconds * execution_count) AS total_cpu
FROM spanner_sys.query_stats_top_hour
GROUP BY text_fingerprint
ORDER BY total_cpu DESC
LIMIT 10;

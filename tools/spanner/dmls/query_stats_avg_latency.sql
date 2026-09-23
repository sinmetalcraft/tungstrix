# spanner_sys.query_stats_top_hour の保持期間全体で平均レイテンシが高いクエリを@limit件取得する
# 同じクエリ(TEXT_FINGERPRINT)が複数の interval にまたがるためGROUP BYで集約している
# total_cpuは interval ごとの avg_cpu_seconds * execution_count の総和、avg_*はexecution_countによる加重平均
# バッチジョブのように実行頻度が低くても1回あたりのLatencyが大きいQueryを拾うため、全期間を対象にしている
# SELECT句は query_stats_total_cpu.sql と揃えているため、ORDER BY以外は変更しないこと
SELECT text_fingerprint,
       ANY_VALUE(text) AS text,
       LOGICAL_OR(text_truncated) AS text_truncated,
       ANY_VALUE(request_tag) AS request_tag,
       SUM(execution_count) AS count,
       IFNULL(SAFE_DIVIDE(SUM(avg_latency_seconds * execution_count), SUM(execution_count)), 0) AS avg_latency,
       IFNULL(SAFE_DIVIDE(SUM(avg_cpu_seconds * execution_count), SUM(execution_count)), 0) AS avg_cpu,
       IFNULL(SUM(avg_cpu_seconds * execution_count), 0) AS total_cpu,
       IFNULL(SAFE_DIVIDE(SUM(avg_rows_scanned * execution_count), SUM(execution_count)), 0) AS avg_rows_scanned,
       IFNULL(SAFE_DIVIDE(SUM(avg_rows * execution_count), SUM(execution_count)), 0) AS avg_rows_returned,
       IFNULL(SAFE_DIVIDE(SUM(avg_bytes * execution_count), SUM(execution_count)), 0) AS avg_bytes
FROM spanner_sys.query_stats_top_hour
-- Cloud Consoleが発行するQueryとINFORMATION_SCHEMAへのQueryは最適化の対象にならないため除外する
-- request_tagが未設定のQueryを落とさないようにIFNULLしている
WHERE IFNULL(request_tag, '') NOT LIKE 'sys_cloud_console_%'
  AND NOT REGEXP_CONTAINS(text, r'(?i)information_schema')
GROUP BY text_fingerprint
ORDER BY avg_latency DESC
LIMIT @limit;

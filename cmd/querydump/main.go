// querydump is spanner_sys.query_stats_top_hourから負荷の高いQueryを取得し、
// SQL(.sql)、QueryPlan(.txt)、Query Statistics(.md)をファイルに出力するlocal用のtool
//
//	go run ./cmd/querydump -project=my-project -instance=my-instance -database=my-db -prefix=hoge
//
// 出力されるファイル名は {prefix}-YYYYMMDD-HHMM-{CPUTOP|LATENCYTOP}{ランキング} で、
// prefixを指定しなかった場合はYYYYMMDDから始まる。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	tungstrixspanner "github.com/sinmetalcraft/tungstrix/tools/spanner"
)

func main() {
	projectID := flag.String("project", "", "Spanner ProjectID (required)")
	instanceID := flag.String("instance", "", "Spanner InstanceID (required)")
	databaseID := flag.String("database", "", "Spanner DatabaseID (required)")
	prefix := flag.String("prefix", "", "出力ファイル名の先頭に付ける文字列 (例: hoge を指定すると hoge-YYYYMMDD-HHMM-CPUTOP01.sql)")
	order := flag.String("order", string(tungstrixspanner.QueryStatsOrderTotalCPU), fmt.Sprintf("Queryの並び順 %v", tungstrixspanner.QueryStatsOrders))
	top := flag.Int64("top", 25, "出力するQueryの件数")
	outDir := flag.String("out", ".", "出力先ディレクトリ")
	flag.Parse()

	if err := run(context.Background(), &config{
		ProjectID:  *projectID,
		InstanceID: *instanceID,
		DatabaseID: *databaseID,
		Prefix:     *prefix,
		Order:      *order,
		Top:        *top,
		OutDir:     *outDir,
	}); err != nil {
		log.Printf("%v", err)
		flag.Usage()
		os.Exit(1)
	}
}

type config struct {
	ProjectID  string
	InstanceID string
	DatabaseID string
	Prefix     string
	Order      string
	Top        int64
	OutDir     string
}

func run(ctx context.Context, cfg *config) error {
	if cfg.ProjectID == "" || cfg.InstanceID == "" || cfg.DatabaseID == "" {
		return errors.New("-project, -instance, -database は必須です")
	}
	if cfg.Top < 1 {
		return fmt.Errorf("-top は1以上を指定してください: %d", cfg.Top)
	}
	order, err := tungstrixspanner.ParseQueryStatsOrder(cfg.Order)
	if err != nil {
		return fmt.Errorf("-order には %v のいずれかを指定してください: %w", tungstrixspanner.QueryStatsOrders, err)
	}

	cli, err := spanner.NewClient(ctx, fmt.Sprintf("projects/%s/instances/%s/databases/%s", cfg.ProjectID, cfg.InstanceID, cfg.DatabaseID))
	if err != nil {
		return fmt.Errorf("failed to create spanner client: %w", err)
	}
	defer cli.Close()

	s, err := tungstrixspanner.NewStatisticsService(ctx, cli)
	if err != nil {
		return fmt.Errorf("failed to create StatisticsService: %w", err)
	}

	queries, err := s.ListQueryStats(ctx, order, cfg.Top)
	if err != nil {
		return fmt.Errorf("failed to list query stats: %w", err)
	}
	if len(queries) == 0 {
		return errors.New("spanner_sys.query_stats_top_hour に対象のQueryがありませんでした")
	}

	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("failed to create out dir %s: %w", cfg.OutDir, err)
	}

	// 全てのファイルで同じタイムスタンプを使いたいので、Query実行後に一度だけ取得する
	timestamp := time.Now().Format("20060102-1504")

	var planFailedCount int
	for i, q := range queries {
		rank := i + 1
		baseName := buildBaseName(cfg.Prefix, timestamp, order, rank)

		if err := writeFile(cfg.OutDir, baseName+".sql", ensureTrailingNewline(q.Text)); err != nil {
			return err
		}

		// QueryParameterを含むQueryやtextが切り詰められているQueryは実行計画を取得できないため、
		// 1件失敗しても中断せずにエラーを記録して次のQueryに進む
		plan, planErr := s.AnalyzeQueryRenderTree(ctx, q.Text)
		if planErr != nil {
			planFailedCount++
			log.Printf("%s: failed to get query plan (text_fingerprint=%d): %v", baseName, q.TextFingerprint, planErr)
			plan = fmt.Sprintf("failed to get query plan: %v", planErr)
		}
		if err := writeFile(cfg.OutDir, baseName+".txt", ensureTrailingNewline(plan)); err != nil {
			return err
		}

		if err := writeFile(cfg.OutDir, baseName+".md", buildStatsMarkdown(baseName, order, rank, q, planErr)); err != nil {
			return err
		}
	}

	log.Printf("wrote %d queries (%d files) to %s", len(queries), len(queries)*3, cfg.OutDir)
	if planFailedCount > 0 {
		log.Printf("%d/%d queries failed to get a query plan", planFailedCount, len(queries))
	}
	return nil
}

// buildBaseName is 拡張子を除いた出力ファイル名を返す
// 例: hoge-20260923-1430-CPUTOP01
func buildBaseName(prefix string, timestamp string, order tungstrixspanner.QueryStatsOrder, rank int) string {
	name := fmt.Sprintf("%s-%s%02d", timestamp, order.Label(), rank)
	if prefix != "" {
		name = prefix + "-" + name
	}
	return name
}

func writeFile(outDir string, name string, body string) error {
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// buildStatsMarkdown is Query Statisticsをmarkdownのtableにしたものを返す
func buildStatsMarkdown(baseName string, order tungstrixspanner.QueryStatsOrder, rank int, q *tungstrixspanner.QueryStats, planErr error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", baseName)
	b.WriteString("spanner_sys.query_stats_top_hour の保持期間全体を TEXT_FINGERPRINT で集約した Query Statistics です。\n")
	b.WriteString("avg_* は execution_count による加重平均、total_cpu は interval ごとの `avg_cpu_seconds * execution_count` の総和です。\n\n")

	fmt.Fprintf(&b, "| item | value |\n|---|---|\n")
	fmt.Fprintf(&b, "| order | %s (%s) |\n", order.Label(), order)
	fmt.Fprintf(&b, "| rank | %d |\n", rank)
	fmt.Fprintf(&b, "| text_fingerprint | %d |\n", q.TextFingerprint)
	fmt.Fprintf(&b, "| request_tag | %s |\n", mdCell(q.RequestTag))
	fmt.Fprintf(&b, "| execution_count | %d |\n", q.Count)
	fmt.Fprintf(&b, "| total_cpu (s) | %.3f |\n", q.TotalCPU)
	fmt.Fprintf(&b, "| avg_cpu (s) | %.6f |\n", q.AvgCPU)
	fmt.Fprintf(&b, "| avg_latency (s) | %.6f |\n", q.AvgLatency)
	fmt.Fprintf(&b, "| avg_rows_scanned | %.1f |\n", q.AvgRowsScanned)
	fmt.Fprintf(&b, "| avg_rows_returned | %.1f |\n", q.AvgRowsReturned)
	fmt.Fprintf(&b, "| avg_bytes | %.1f |\n", q.AvgBytes)
	fmt.Fprintf(&b, "| text_truncated | %t |\n", q.TextTruncated)
	b.WriteString("\n")

	// Scanした行数のうち何行が返っているかは、不要なScanが多いQueryを見つける手掛かりになる
	switch {
	case q.AvgRowsScanned == 0:
		// SPANNER_SYSへのQueryやDMLなどavg_rows_scannedが0のものは何も言えないので出力しない
	case q.AvgRowsReturned == 0:
		// 存在しないことを確認するQueryは倍率が計算できないが、Scanが無駄になっている典型なので必ず出力する
		fmt.Fprintf(&b, "avg_rows_scanned が %.1f 行ある一方で、avg_rows_returned は 0 行です。存在しないことの確認などでScanが無駄になっている可能性があります。\n\n", q.AvgRowsScanned)
	default:
		fmt.Fprintf(&b, "avg_rows_scanned は avg_rows_returned の %.1f 倍です。\n\n", q.AvgRowsScanned/q.AvgRowsReturned)
	}

	fmt.Fprintf(&b, "## files\n\n- SQL: `%s.sql`\n- QueryPlan: `%s.txt`\n", baseName, baseName)
	if q.TextTruncated {
		b.WriteString("\n## note\n\ntextが切り詰められているため、.sqlは完全なSQLではありません。\n")
	}
	if planErr != nil {
		fmt.Fprintf(&b, "\n## note\n\nQueryPlanの取得に失敗しました: %v\n", planErr)
	}
	return b.String()
}

// mdCell is markdownのtableのcellとして出力できる文字列に変換する
func mdCell(v string) string {
	if v == "" {
		return "(empty)"
	}
	return strings.ReplaceAll(v, "|", "\\|")
}

func ensureTrailingNewline(v string) string {
	if v == "" || strings.HasSuffix(v, "\n") {
		return v
	}
	return v + "\n"
}

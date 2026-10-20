// readdump is spanner_sys.read_stats_top_hourからCPU使用量の大きいReadを取得し、markdownに出力するlocal用のtool
// Read API(ReadRow, Readなど)で読んだものが対象で、SQLのQueryはquerydumpで確認する
//
//	go run ./cmd/readdump -project=my-project -instance=my-instance -database=my-db -prefix=hoge
//
// 出力されるファイル名は {prefix}-YYYYMMDD-HHMM-READTOP.md で、
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
	databaseRole := flag.String("database-role", "", "Fine-grained access controlのdatabase roleを指定して接続する (例: spanner_sys_reader)")
	prefix := flag.String("prefix", "", "出力ファイル名の先頭に付ける文字列 (例: hoge を指定すると hoge-YYYYMMDD-HHMM-READTOP.md)")
	top := flag.Int64("top", 25, "出力するReadの件数")
	outDir := flag.String("out", ".", "出力先ディレクトリ")
	var filters filterFlags
	flag.Var(&filters, "filter", fmt.Sprintf("Readの絞り込み条件をkey=valueで指定する (複数指定可、ANDで結合) 指定できるkey %v", tungstrixspanner.QueryStatsFilterKeys))
	flag.Parse()

	if err := run(context.Background(), &config{
		ProjectID:    *projectID,
		InstanceID:   *instanceID,
		DatabaseID:   *databaseID,
		DatabaseRole: *databaseRole,
		Prefix:       *prefix,
		Top:          *top,
		OutDir:       *outDir,
		Filters:      filters,
	}); err != nil {
		log.Printf("%v", err)
		// 引数の指定ミス以外でUsageを出しても紛らわしいので、そのときだけ出す
		var ue usageError
		if errors.As(err, &ue) {
			flag.Usage()
		}
		os.Exit(1)
	}
}

// usageError is flagの指定が誤っていることを示すエラー
type usageError struct {
	err error
}

func (e usageError) Error() string {
	return e.err.Error()
}

func (e usageError) Unwrap() error {
	return e.err
}

func newUsageErrorf(format string, args ...any) error {
	return usageError{err: fmt.Errorf(format, args...)}
}

// filterFlags is 繰り返し指定できる-filter
type filterFlags []string

func (f *filterFlags) String() string {
	return strings.Join(*f, " ")
}

func (f *filterFlags) Set(v string) error {
	*f = append(*f, v)
	return nil
}

type config struct {
	ProjectID  string
	InstanceID string
	DatabaseID string
	// DatabaseRole is Fine-grained access controlで使うdatabase role。空の場合はIAMの権限で接続する
	DatabaseRole string
	Prefix       string
	Top          int64
	OutDir       string
	Filters      []string
}

func run(ctx context.Context, cfg *config) error {
	if cfg.ProjectID == "" || cfg.InstanceID == "" || cfg.DatabaseID == "" {
		return newUsageErrorf("-project, -instance, -database は必須です")
	}
	if cfg.Top < 1 {
		return newUsageErrorf("-top は1以上を指定してください: %d", cfg.Top)
	}

	filters := make([]tungstrixspanner.QueryStatsFilter, 0, len(cfg.Filters))
	for _, v := range cfg.Filters {
		f, err := tungstrixspanner.ParseQueryStatsFilter(v)
		if err != nil {
			return newUsageErrorf("-filter: %w", err)
		}
		filters = append(filters, f)
	}

	statsDB := fmt.Sprintf("projects/%s/instances/%s/databases/%s", cfg.ProjectID, cfg.InstanceID, cfg.DatabaseID)
	cli, err := spanner.NewClientWithConfig(ctx, statsDB, spanner.ClientConfig{
		DatabaseRole: cfg.DatabaseRole,
	})
	if err != nil {
		return fmt.Errorf("failed to create spanner client: %w", err)
	}
	defer cli.Close()

	s, err := tungstrixspanner.NewStatisticsService(ctx, cli)
	if err != nil {
		return fmt.Errorf("failed to create StatisticsService: %w", err)
	}

	reads, err := s.ListReadStats(ctx, cfg.Top, filters...)
	if err != nil {
		return fmt.Errorf("failed to list read stats: %w", err)
	}
	if len(reads) == 0 {
		return fmt.Errorf("spanner_sys.read_stats_top_hour に対象のReadがありませんでした (filter: %s)", tungstrixspanner.FormatQueryStatsFilters(filters))
	}

	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("failed to create out dir %s: %w", cfg.OutDir, err)
	}

	timestamp := time.Now().Format("20060102-1504")
	name := buildFileName(cfg.Prefix, timestamp)
	path := filepath.Join(cfg.OutDir, name)
	if err := os.WriteFile(path, []byte(buildMarkdown(name, statsDB, filters, reads)), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	log.Printf("wrote %d reads to %s (filter: %s)", len(reads), path, tungstrixspanner.FormatQueryStatsFilters(filters))
	return nil
}

// buildFileName is 出力ファイル名を返す
// 例: hoge-20261020-1530-READTOP.md
func buildFileName(prefix string, timestamp string) string {
	name := timestamp + "-READTOP.md"
	if prefix != "" {
		name = prefix + "-" + name
	}
	return name
}

// readTables is read_columnsに含まれるTable名を、重複を除いて出現順に返す
// read_columnsは "UserBalanceAdditions.CreatedAt" のように "Table名.Column名" の形式で入っている
func readTables(columns []string) []string {
	var tables []string
	seen := map[string]bool{}
	for _, c := range columns {
		table, _, _ := strings.Cut(c, ".")
		if seen[table] {
			continue
		}
		seen[table] = true
		tables = append(tables, table)
	}
	return tables
}

// buildMarkdown is ReadStatsの一覧と、Readごとの詳細をmarkdownにしたものを返す
func buildMarkdown(name string, statsDB string, filters []tungstrixspanner.QueryStatsFilter, reads []*tungstrixspanner.ReadStats) string {
	var total float64
	for _, r := range reads {
		total += r.TotalCPU
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSuffix(name, ".md"))
	b.WriteString("spanner_sys.read_stats_top_hour の保持期間全体を FPRINT で集約した Read Statistics です。\n")
	b.WriteString("Read API(ReadRow, Readなど)で読んだものが対象で、SQLのQueryは含まれません。\n")
	b.WriteString("avg_* は execution_count による加重平均、total_cpu は interval ごとの `avg_cpu_seconds * execution_count` の総和です。\n")
	fmt.Fprintf(&b, "share は、今回出力した%d件のtotal_cpuの合計に対する割合です。DB全体のCPU使用量に対する割合ではありません。\n\n", len(reads))

	fmt.Fprintf(&b, "- database: `%s`\n", statsDB)
	fmt.Fprintf(&b, "- filter: %s\n\n", tungstrixspanner.FormatQueryStatsFilters(filters))

	b.WriteString("## ranking\n\n")
	b.WriteString("| rank | table | read_type | execution_count | rw_tx (%) | total_cpu (s) | share | cumulative | avg_cpu (s) | avg_rows | avg_locking_delay (s) |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	var cumulative float64
	for i, r := range reads {
		var share float64
		if total > 0 {
			share = r.TotalCPU / total
		}
		cumulative += share
		fmt.Fprintf(&b, "| %d | %s | %s | %d | %.1f%% | %.3f | %.1f%% | %.1f%% | %.6f | %.1f | %.6f |\n",
			i+1, mdCell(strings.Join(readTables(r.ReadColumns), ", ")), mdCell(r.ReadType), r.Count, ratio(r.RWCount, r.Count)*100,
			r.TotalCPU, share*100, cumulative*100, r.AvgCPU, r.AvgRows, r.AvgLockingDelay)
	}

	for i, r := range reads {
		fmt.Fprintf(&b, "\n## %d. %s\n\n", i+1, strings.Join(readTables(r.ReadColumns), ", "))
		b.WriteString("| item | value |\n|---|---|\n")
		fmt.Fprintf(&b, "| fprint | %d |\n", r.Fprint)
		fmt.Fprintf(&b, "| read_type | %s |\n", mdCell(r.ReadType))
		fmt.Fprintf(&b, "| request_tag | %s |\n", mdCell(r.RequestTag))
		fmt.Fprintf(&b, "| execution_count | %d |\n", r.Count)
		fmt.Fprintf(&b, "| run_in_rw_transaction_execution_count | %d |\n", r.RWCount)
		fmt.Fprintf(&b, "| total_cpu (s) | %.3f |\n", r.TotalCPU)
		fmt.Fprintf(&b, "| avg_cpu (s) | %.6f |\n", r.AvgCPU)
		fmt.Fprintf(&b, "| avg_rows | %.1f |\n", r.AvgRows)
		fmt.Fprintf(&b, "| avg_bytes | %.1f |\n", r.AvgBytes)
		fmt.Fprintf(&b, "| avg_locking_delay (s) | %.6f |\n", r.AvgLockingDelay)
		fmt.Fprintf(&b, "| avg_client_wait (s) | %.6f |\n", r.AvgClientWait)
		fmt.Fprintf(&b, "| avg_leader_refresh_delay (s) | %.6f |\n", r.AvgLeaderRefreshDelay)
		fmt.Fprintf(&b, "| avg_disk_io_cost | %.3f |\n", r.AvgDiskIOCost)
		b.WriteString("\nread_columns:\n\n")
		for _, c := range r.ReadColumns {
			fmt.Fprintf(&b, "- `%s`\n", c)
		}
	}
	return b.String()
}

// ratio is 0除算を避けてn/dを返す
func ratio(n int64, d int64) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// mdCell is markdownのtableのcellとして出力できる文字列に変換する
func mdCell(v string) string {
	if v == "" {
		return "(empty)"
	}
	return strings.ReplaceAll(v, "|", "\\|")
}

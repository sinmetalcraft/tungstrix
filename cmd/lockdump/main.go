// lockdump is spanner_sys.lock_stats_top_hourからLockの待ち時間が大きいRowを取得し、markdownに出力するlocal用のtool
//
//	go run ./cmd/lockdump -project=my-project -instance=my-instance -database=my-db -prefix=hoge
//
// 出力されるファイル名は {prefix}-YYYYMMDD-HHMM-LOCKTOP.md で、
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
	prefix := flag.String("prefix", "", "出力ファイル名の先頭に付ける文字列 (例: hoge を指定すると hoge-YYYYMMDD-HHMM-LOCKTOP.md)")
	top := flag.Int64("top", 25, "出力するRowの件数")
	outDir := flag.String("out", ".", "出力先ディレクトリ")
	var filters filterFlags
	flag.Var(&filters, "filter", fmt.Sprintf("Rowの絞り込み条件をkey=valueで指定する (複数指定可、ANDで結合) 指定できるkey %v", tungstrixspanner.QueryStatsFilterKeys))
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

	locks, err := s.ListLockStats(ctx, cfg.Top, filters...)
	if err != nil {
		return fmt.Errorf("failed to list lock stats: %w", err)
	}
	if len(locks) == 0 {
		return fmt.Errorf("spanner_sys.lock_stats_top_hour に対象のRowがありませんでした (filter: %s)", tungstrixspanner.FormatQueryStatsFilters(filters))
	}

	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("failed to create out dir %s: %w", cfg.OutDir, err)
	}

	timestamp := time.Now().Format("20060102-1504")
	name := buildFileName(cfg.Prefix, timestamp)
	path := filepath.Join(cfg.OutDir, name)
	if err := os.WriteFile(path, []byte(buildMarkdown(name, statsDB, filters, locks)), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	log.Printf("wrote %d rows to %s (filter: %s)", len(locks), path, tungstrixspanner.FormatQueryStatsFilters(filters))
	return nil
}

// buildFileName is 出力ファイル名を返す
// 例: hoge-20261020-1530-LOCKTOP.md
func buildFileName(prefix string, timestamp string) string {
	name := timestamp + "-LOCKTOP.md"
	if prefix != "" {
		name = prefix + "-" + name
	}
	return name
}

// keyTable is row_range_start_keyからTable名を返す
// row_range_start_keyは "userbalanceconsumptions(2b360e4b-...)" のように小文字のTable名とKeyの形式で入っている
func keyTable(key string) string {
	table, _, ok := strings.Cut(key, "(")
	if !ok {
		return ""
	}
	return table
}

// buildMarkdown is LockStatsの一覧と、Rowごとのsample_lock_requestsをmarkdownにしたものを返す
func buildMarkdown(name string, statsDB string, filters []tungstrixspanner.QueryStatsFilter, locks []*tungstrixspanner.LockStats) string {
	var total float64
	for _, l := range locks {
		total += l.TotalLockWait
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSuffix(name, ".md"))
	b.WriteString("spanner_sys.lock_stats_top_hour の保持期間全体を row_range_start_key で集約した Lock Statistics です。\n")
	b.WriteString("total_lock_wait は interval ごとの lock_wait_seconds の総和です。\n")
	b.WriteString("sample_lock_requests は全 interval の sample から重複を除いたもので、Lockを要求した全てのColumnではありません。\n")
	fmt.Fprintf(&b, "share は、今回出力した%d件のtotal_lock_waitの合計に対する割合です。\n\n", len(locks))

	fmt.Fprintf(&b, "- database: `%s`\n", statsDB)
	fmt.Fprintf(&b, "- filter: %s\n\n", tungstrixspanner.FormatQueryStatsFilters(filters))

	b.WriteString("## ranking\n\n")
	b.WriteString("| rank | table | row_range_start_key | total_lock_wait (s) | share | cumulative | intervals | first_interval_end | last_interval_end |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	var cumulative float64
	for i, l := range locks {
		var share float64
		if total > 0 {
			share = l.TotalLockWait / total
		}
		cumulative += share
		key := l.RowRangeStartKeyString()
		fmt.Fprintf(&b, "| %d | %s | `%s` | %.3f | %.1f%% | %.1f%% | %d | %s | %s |\n",
			i+1, mdCell(keyTable(key)), strings.ReplaceAll(key, "|", "\\|"), l.TotalLockWait, share*100, cumulative*100,
			l.IntervalCount, l.FirstIntervalEnd.UTC().Format(time.RFC3339), l.LastIntervalEnd.UTC().Format(time.RFC3339))
	}

	for i, l := range locks {
		key := l.RowRangeStartKeyString()
		fmt.Fprintf(&b, "\n## %d. `%s`\n\n", i+1, key)
		b.WriteString("| column | lock_mode | transaction_tag |\n|---|---|---|\n")
		for _, r := range l.UniqueSampleLockRequests() {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", mdCell(r.Column), mdCell(r.LockMode), mdCell(r.TransactionTag))
		}
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

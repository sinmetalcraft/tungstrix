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
	paramsPath := flag.String("params", "", "QueryParameterの型を指定するJSONファイル (実行計画の取得に失敗した場合に雛形を出力します)")
	databaseRole := flag.String("database-role", "", "Fine-grained access controlのdatabase roleを指定して接続する (例: spanner_sys_reader)")
	planProjectID := flag.String("plan-project", "", "実行計画を取得するSpanner ProjectID (省略時は-projectと同じ)")
	planInstanceID := flag.String("plan-instance", "", "実行計画を取得するSpanner InstanceID (省略時は-instanceと同じ)")
	planDatabaseID := flag.String("plan-database", "", "実行計画を取得するSpanner DatabaseID。指定するとQuery Statisticsとは別のDBでEXPLAINする (例: 同じschemaのdev環境のDB)")
	var filters filterFlags
	flag.Var(&filters, "filter", fmt.Sprintf("Queryの絞り込み条件をkey=valueで指定する (複数指定可、ANDで結合) 指定できるkey %v", tungstrixspanner.QueryStatsFilterKeys))
	flag.Parse()

	if err := run(context.Background(), &config{
		ProjectID:      *projectID,
		InstanceID:     *instanceID,
		DatabaseID:     *databaseID,
		Prefix:         *prefix,
		Order:          *order,
		Top:            *top,
		OutDir:         *outDir,
		ParamsPath:     *paramsPath,
		DatabaseRole:   *databaseRole,
		PlanProjectID:  *planProjectID,
		PlanInstanceID: *planInstanceID,
		PlanDatabaseID: *planDatabaseID,
		Filters:        filters,
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
	Prefix     string
	Order      string
	Top        int64
	OutDir     string
	ParamsPath string
	// DatabaseRole is Fine-grained access controlで使うdatabase role。空の場合はIAMの権限で接続する
	DatabaseRole string
	// PlanProjectID, PlanInstanceID, PlanDatabaseID is 実行計画を取得するDB
	// spanner_sys_readerのようにtableを読めないroleで統計を取得する場合に、同じschemaの別DBでEXPLAINするために使う
	PlanProjectID  string
	PlanInstanceID string
	PlanDatabaseID string
	Filters        []string
}

// queryResult is 1Queryの出力内容
type queryResult struct {
	BaseName string
	Rank     int
	Stats    *tungstrixspanner.QueryStats
	// ParamNames is SQLに含まれるQueryParameter名
	ParamNames []string
	// PlanErr is 実行計画の取得に失敗した場合のエラー
	PlanErr error
	// CPUShare is 出力したQuery全体のtotal_cpuに対する、このQueryのtotal_cpuの割合(0〜1)
	CPUShare float64
	// CumulativeCPUShare is 1位からこのQueryまでのCPUShareの累積(0〜1)
	CumulativeCPUShare float64
}

// cpuShare is 1QueryのCPU使用量の割合
type cpuShare struct {
	Share      float64
	Cumulative float64
}

// calcCPUShares is 出力するQueryのtotal_cpuの合計を分母にして、各Queryの割合と上位からの累積を返す
// spanner_sys.query_stats_top_hourは各intervalの上位Queryしか保持していないため、
// DB全体のCPU使用量に対する割合ではなく、取得したQueryの中での割合としている
func calcCPUShares(queries []*tungstrixspanner.QueryStats) []cpuShare {
	var total float64
	for _, q := range queries {
		total += q.TotalCPU
	}
	shares := make([]cpuShare, len(queries))
	if total == 0 {
		return shares
	}
	var cumulative float64
	for i, q := range queries {
		share := q.TotalCPU / total
		cumulative += share
		shares[i] = cpuShare{Share: share, Cumulative: cumulative}
	}
	return shares
}

func run(ctx context.Context, cfg *config) error {
	if cfg.ProjectID == "" || cfg.InstanceID == "" || cfg.DatabaseID == "" {
		return newUsageErrorf("-project, -instance, -database は必須です")
	}
	if cfg.Top < 1 {
		return newUsageErrorf("-top は1以上を指定してください: %d", cfg.Top)
	}
	order, err := tungstrixspanner.ParseQueryStatsOrder(cfg.Order)
	if err != nil {
		return newUsageErrorf("-order には %v のいずれかを指定してください: %w", tungstrixspanner.QueryStatsOrders, err)
	}

	filters := make([]tungstrixspanner.QueryStatsFilter, 0, len(cfg.Filters))
	for _, v := range cfg.Filters {
		f, err := tungstrixspanner.ParseQueryStatsFilter(v)
		if err != nil {
			return newUsageErrorf("-filter: %w", err)
		}
		filters = append(filters, f)
	}

	var params queryParams
	if cfg.ParamsPath != "" {
		params, err = loadQueryParams(cfg.ParamsPath)
		if err != nil {
			return err
		}
	}

	statsDB := buildDatabaseName(cfg.ProjectID, cfg.InstanceID, cfg.DatabaseID)
	planDB := resolvePlanDatabase(cfg)

	// spanner_sys_readerはSPANNER_SYSのtableしか読めないため、実行計画の取得はtableの権限不足で失敗することがある
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

	// 実行計画を別のDBで取得する場合は、そのDBのtableを読めるようにIAMの権限で接続する
	planService := s
	if planDB != statsDB {
		planCli, err := spanner.NewClient(ctx, planDB)
		if err != nil {
			return fmt.Errorf("failed to create spanner client for plan %s: %w", planDB, err)
		}
		defer planCli.Close()

		planService, err = tungstrixspanner.NewStatisticsService(ctx, planCli)
		if err != nil {
			return fmt.Errorf("failed to create StatisticsService for plan: %w", err)
		}
		log.Printf("query plan is taken from %s", planDB)
	}

	queries, err := s.ListQueryStats(ctx, order, cfg.Top, filters...)
	if err != nil {
		return fmt.Errorf("failed to list query stats: %w", err)
	}
	if len(queries) == 0 {
		return fmt.Errorf("spanner_sys.query_stats_top_hour に対象のQueryがありませんでした (filter: %s)", tungstrixspanner.FormatQueryStatsFilters(filters))
	}

	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("failed to create out dir %s: %w", cfg.OutDir, err)
	}

	// 全てのファイルで同じタイムスタンプを使いたいので、Query実行後に一度だけ取得する
	timestamp := time.Now().Format("20060102-1504")

	shares := calcCPUShares(queries)
	results := make([]*queryResult, 0, len(queries))
	for i, q := range queries {
		r := &queryResult{
			BaseName:           buildBaseName(cfg.Prefix, timestamp, order, i+1),
			Rank:               i + 1,
			Stats:              q,
			ParamNames:         extractQueryParamNames(q.Text),
			CPUShare:           shares[i].Share,
			CumulativeCPUShare: shares[i].Cumulative,
		}
		results = append(results, r)

		if err := writeFile(cfg.OutDir, r.BaseName+".sql", ensureTrailingNewline(q.Text)); err != nil {
			return err
		}

		boundParams, err := params.bindFor(q.TextFingerprint)
		if err != nil {
			return fmt.Errorf("%s (text_fingerprint=%d): %w", r.BaseName, q.TextFingerprint, err)
		}

		// DMLやQueryParameterの型が決まらないQueryは実行計画を取得できないため、
		// 1件失敗しても中断せずにエラーを記録して次のQueryに進む
		plan, planErr := planService.AnalyzeQueryRenderTreeWithParams(ctx, q.Text, boundParams)
		if planErr != nil {
			r.PlanErr = planErr
			log.Printf("%s: failed to get query plan (text_fingerprint=%d): %v", r.BaseName, q.TextFingerprint, planErr)
			plan = fmt.Sprintf("failed to get query plan: %v", planErr)
		}
		if err := writeFile(cfg.OutDir, r.BaseName+".txt", ensureTrailingNewline(plan)); err != nil {
			return err
		}

		if err := writeFile(cfg.OutDir, r.BaseName+".md", buildStatsMarkdown(order, filters, len(queries), statsDB, planDB, r)); err != nil {
			return err
		}
	}

	log.Printf("wrote %d queries (%d files) to %s (filter: %s)", len(results), len(results)*3, cfg.OutDir, tungstrixspanner.FormatQueryStatsFilters(filters))
	return reportFailures(cfg, timestamp, results)
}

// reportFailures is 実行計画を取得できなかったQueryをまとめてログに出し、-paramsの雛形を出力する
func reportFailures(cfg *config, timestamp string, results []*queryResult) error {
	var failed []*queryResult
	for _, r := range results {
		if r.PlanErr != nil {
			failed = append(failed, r)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	log.Printf("%d/%d queries failed to get a query plan", len(failed), len(results))

	template, err := buildParamsTemplate(failed)
	if err != nil {
		return fmt.Errorf("failed to build params template: %w", err)
	}
	// QueryParameterを含まないQuery(DMLなど)だけが失敗した場合は、雛形を出しても意味がない
	if template == nil {
		return nil
	}

	name := buildParamsTemplateName(cfg.Prefix, timestamp)
	if err := writeFile(cfg.OutDir, name, string(template)+"\n"); err != nil {
		return err
	}
	log.Printf("wrote params template: %s", filepath.Join(cfg.OutDir, name))
	log.Printf("  typeを埋めて -params=%s を付けて再実行すると実行計画を取得できます", filepath.Join(cfg.OutDir, name))
	log.Printf("  指定可能な型: %v", supportedParamTypes())
	for _, r := range failed {
		if len(r.ParamNames) == 0 {
			continue
		}
		log.Printf("  %d -> %s.sql", r.Stats.TextFingerprint, r.BaseName)
	}
	return nil
}

func buildDatabaseName(projectID string, instanceID string, databaseID string) string {
	return fmt.Sprintf("projects/%s/instances/%s/databases/%s", projectID, instanceID, databaseID)
}

// resolvePlanDatabase is 実行計画を取得するDBのDatabaseNameを返す
// -plan-databaseを指定しなかった場合はQuery Statisticsと同じDBで、
// -plan-project, -plan-instanceを省略した場合は-project, -instanceと同じとする
func resolvePlanDatabase(cfg *config) string {
	if cfg.PlanDatabaseID == "" {
		return buildDatabaseName(cfg.ProjectID, cfg.InstanceID, cfg.DatabaseID)
	}
	projectID, instanceID := cfg.PlanProjectID, cfg.PlanInstanceID
	if projectID == "" {
		projectID = cfg.ProjectID
	}
	if instanceID == "" {
		instanceID = cfg.InstanceID
	}
	return buildDatabaseName(projectID, instanceID, cfg.PlanDatabaseID)
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

// buildParamsTemplateName is -paramsの雛形のファイル名を返す
// keyがtext_fingerprintなのでorderに依存せず使い回せる
func buildParamsTemplateName(prefix string, timestamp string) string {
	name := timestamp + "-params.json"
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
// queryCountは割合の分母にしたQueryの件数で、-topより少ない件数しか取得できなかった場合もあるので実際の件数を渡す
// statsDB, planDBはQuery Statisticsと実行計画を取得したDBで、別のDBで実行計画を取得した場合はその旨を書いておく
func buildStatsMarkdown(order tungstrixspanner.QueryStatsOrder, filters []tungstrixspanner.QueryStatsFilter, queryCount int, statsDB string, planDB string, r *queryResult) string {
	baseName, rank, q := r.BaseName, r.Rank, r.Stats

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", baseName)
	b.WriteString("spanner_sys.query_stats_top_hour の保持期間全体を TEXT_FINGERPRINT で集約した Query Statistics です。\n")
	b.WriteString("avg_* は execution_count による加重平均、total_cpu は interval ごとの `avg_cpu_seconds * execution_count` の総和です。\n")
	fmt.Fprintf(&b, "total_cpu share は、今回出力した%d件のtotal_cpuの合計に対する割合です。DB全体のCPU使用量に対する割合ではありません。\n\n", queryCount)

	fmt.Fprintf(&b, "| item | value |\n|---|---|\n")
	fmt.Fprintf(&b, "| order | %s (%s) |\n", order.Label(), order)
	fmt.Fprintf(&b, "| rank | %d |\n", rank)
	fmt.Fprintf(&b, "| filter | %s |\n", mdCell(tungstrixspanner.FormatQueryStatsFilters(filters)))
	fmt.Fprintf(&b, "| stats_database | %s |\n", statsDB)
	fmt.Fprintf(&b, "| plan_database | %s |\n", planDB)
	fmt.Fprintf(&b, "| text_fingerprint | %d |\n", q.TextFingerprint)
	fmt.Fprintf(&b, "| request_tag | %s |\n", mdCell(q.RequestTag))
	fmt.Fprintf(&b, "| execution_count | %d |\n", q.Count)
	fmt.Fprintf(&b, "| total_cpu (s) | %.3f |\n", q.TotalCPU)
	fmt.Fprintf(&b, "| total_cpu share (TOP%d) | %.1f%% |\n", queryCount, r.CPUShare*100)
	// latencyの順に並べたときの累積は意味を持たないので、CPUの順に並べたときだけ出力する
	if order == tungstrixspanner.QueryStatsOrderTotalCPU {
		fmt.Fprintf(&b, "| total_cpu cumulative share (TOP%d) | %.1f%% |\n", queryCount, r.CumulativeCPUShare*100)
	}
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
	// Optimizerはtableの統計情報も使って実行計画を作るため、データ量が違うDBでは本番と異なる実行計画になることがある
	if statsDB != planDB {
		fmt.Fprintf(&b, "\nQueryPlanはQuery Statisticsとは別のDB `%s` でEXPLAINしたものです。schemaやデータ量が異なる場合は本番と異なる実行計画になることがあります。\n", planDB)
	}

	if len(r.ParamNames) > 0 {
		b.WriteString("\n## query parameters\n\n")
		for _, name := range r.ParamNames {
			fmt.Fprintf(&b, "- `@%s`\n", name)
		}
	}

	if q.TextTruncated {
		b.WriteString("\n## note\n\ntextが切り詰められているため、.sqlは完全なSQLではありません。\n")
	}
	if r.PlanErr != nil {
		fmt.Fprintf(&b, "\n## note\n\nQueryPlanの取得に失敗しました。\n\n```\n%v\n```\n", r.PlanErr)
		if len(r.ParamNames) > 0 {
			fmt.Fprintf(&b, "\nQueryParameterの型が決まらないことが原因の場合は、-paramsに渡すJSONで`%d`に型を指定してください。\n", q.TextFingerprint)
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

func ensureTrailingNewline(v string) string {
	if v == "" || strings.HasSuffix(v, "\n") {
		return v
	}
	return v + "\n"
}

package main

import (
	"math"
	"testing"

	tungstrixspanner "github.com/sinmetalcraft/tungstrix/tools/spanner"
)

func TestBuildBaseName(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		order  tungstrixspanner.QueryStatsOrder
		rank   int
		want   string
	}{
		{
			name:   "prefixなし",
			prefix: "",
			order:  tungstrixspanner.QueryStatsOrderTotalCPU,
			rank:   1,
			want:   "20260923-1430-CPUTOP01",
		},
		{
			name:   "prefixあり",
			prefix: "hoge",
			order:  tungstrixspanner.QueryStatsOrderTotalCPU,
			rank:   2,
			want:   "hoge-20260923-1430-CPUTOP02",
		},
		{
			name:   "2桁のランキング",
			prefix: "hoge",
			order:  tungstrixspanner.QueryStatsOrderTotalCPU,
			rank:   25,
			want:   "hoge-20260923-1430-CPUTOP25",
		},
		{
			name:   "latency",
			prefix: "hoge",
			order:  tungstrixspanner.QueryStatsOrderAvgLatency,
			rank:   1,
			want:   "hoge-20260923-1430-LATENCYTOP01",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := buildBaseName(tt.prefix, "20260923-1430", tt.order, tt.rank)
			if got != tt.want {
				t.Errorf("buildBaseName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseQueryStatsOrder(t *testing.T) {
	for _, order := range tungstrixspanner.QueryStatsOrders {
		if order.Label() == "" {
			t.Errorf("QueryStatsOrder %q has no Label()", order)
		}
		got, err := tungstrixspanner.ParseQueryStatsOrder(string(order))
		if err != nil {
			t.Errorf("ParseQueryStatsOrder(%q) returned err: %v", order, err)
		}
		if got != order {
			t.Errorf("ParseQueryStatsOrder(%q) = %q", order, got)
		}
	}

	if _, err := tungstrixspanner.ParseQueryStatsOrder("unknown"); err == nil {
		t.Error("ParseQueryStatsOrder(\"unknown\") should return err")
	}
}

func TestCalcCPUShares(t *testing.T) {
	queries := []*tungstrixspanner.QueryStats{
		{TotalCPU: 60},
		{TotalCPU: 30},
		{TotalCPU: 10},
	}
	want := []cpuShare{
		{Share: 0.6, Cumulative: 0.6},
		{Share: 0.3, Cumulative: 0.9},
		{Share: 0.1, Cumulative: 1.0},
	}
	got := calcCPUShares(queries)
	if len(got) != len(want) {
		t.Fatalf("len(calcCPUShares()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(got[i].Share-want[i].Share) > 1e-9 || math.Abs(got[i].Cumulative-want[i].Cumulative) > 1e-9 {
			t.Errorf("calcCPUShares()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCalcCPUSharesZeroTotal(t *testing.T) {
	// total_cpuが全て0のときに0除算でNaNにならないこと
	got := calcCPUShares([]*tungstrixspanner.QueryStats{{TotalCPU: 0}, {TotalCPU: 0}})
	for i, s := range got {
		if s.Share != 0 || s.Cumulative != 0 {
			t.Errorf("calcCPUShares()[%d] = %+v, want zero", i, s)
		}
	}
}

func TestResolvePlanDatabase(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config
		want string
	}{
		{
			name: "plan-databaseなし",
			cfg:  &config{ProjectID: "p", InstanceID: "i", DatabaseID: "d"},
			want: "projects/p/instances/i/databases/d",
		},
		{
			name: "plan-databaseのみ指定",
			cfg:  &config{ProjectID: "p", InstanceID: "i", DatabaseID: "d", PlanDatabaseID: "pd"},
			want: "projects/p/instances/i/databases/pd",
		},
		{
			name: "全て指定",
			cfg:  &config{ProjectID: "p", InstanceID: "i", DatabaseID: "d", PlanProjectID: "pp", PlanInstanceID: "pi", PlanDatabaseID: "pd"},
			want: "projects/pp/instances/pi/databases/pd",
		},
		{
			// -plan-databaseがない場合は-plan-projectだけ指定しても同じDBを使う
			name: "plan-projectのみ指定",
			cfg:  &config{ProjectID: "p", InstanceID: "i", DatabaseID: "d", PlanProjectID: "pp"},
			want: "projects/p/instances/i/databases/d",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvePlanDatabase(tt.cfg); got != tt.want {
				t.Errorf("resolvePlanDatabase() = %q, want %q", got, tt.want)
			}
		})
	}
}

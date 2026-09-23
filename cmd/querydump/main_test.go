package main

import (
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

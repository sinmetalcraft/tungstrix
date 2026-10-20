package main

import "testing"

func TestKeyTable(t *testing.T) {
	cases := map[string]string{
		"userbalanceconsumptions(2b360e4b)": "userbalanceconsumptions",
		"accounts(1,2)":                     "accounts",
		"0xff01":                            "",
	}
	for key, want := range cases {
		if got := keyTable(key); got != want {
			t.Errorf("keyTable(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestBuildFileName(t *testing.T) {
	if got := buildFileName("hoge", "20261020-1530"); got != "hoge-20261020-1530-LOCKTOP.md" {
		t.Errorf("buildFileName() = %q", got)
	}
}

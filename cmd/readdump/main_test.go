package main

import (
	"reflect"
	"testing"
)

func TestReadTables(t *testing.T) {
	got := readTables([]string{"A.X", "A.Y", "B.Z", "A.W"})
	if want := []string{"A", "B"}; !reflect.DeepEqual(got, want) {
		t.Errorf("readTables() = %v, want %v", got, want)
	}
}

func TestBuildFileName(t *testing.T) {
	if got := buildFileName("", "20261020-1530"); got != "20261020-1530-READTOP.md" {
		t.Errorf("buildFileName() = %q", got)
	}
	if got := buildFileName("hoge", "20261020-1530"); got != "hoge-20261020-1530-READTOP.md" {
		t.Errorf("buildFileName() = %q", got)
	}
}

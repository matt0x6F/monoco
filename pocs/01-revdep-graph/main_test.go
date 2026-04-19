package main

import (
	"sort"
	"testing"
	"time"

	"github.com/matt0x6f/monoco/internal/fixture"
)

func TestAffectedSet_simpleFixture(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
			{Name: "auth"},
		},
	})

	start := time.Now()
	g, err := BuildGraph(fx.Root)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Errorf("BuildGraph took %v; want <1s", elapsed)
	}
	t.Logf("BuildGraph elapsed: %v", elapsed)

	cases := []struct {
		touched string
		want    []string
	}{
		{"example.com/mono/storage", []string{"example.com/mono/api", "example.com/mono/storage"}},
		{"example.com/mono/auth", []string{"example.com/mono/auth"}},
		{"example.com/mono/api", []string{"example.com/mono/api"}},
	}
	for _, tc := range cases {
		got := g.Affected([]string{tc.touched})
		sort.Strings(got)
		if !equalStrings(got, tc.want) {
			t.Errorf("Affected(%q) = %v; want %v", tc.touched, got, tc.want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

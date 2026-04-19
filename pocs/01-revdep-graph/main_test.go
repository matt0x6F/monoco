package main

import (
	"fmt"
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

func TestAffectedSet_scale50(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test skipped under -short")
	}

	// Chain of 50 modules: core <- mod00 <- mod01 <- ... <- mod48.
	// Worst case for reverse-dep walks.
	specs := []fixture.ModuleSpec{{Name: "core"}}
	prev := "core"
	for i := 0; i < 49; i++ {
		name := fmt.Sprintf("mod%02d", i)
		specs = append(specs, fixture.ModuleSpec{Name: name, DependsOn: []string{prev}})
		prev = name
	}
	fx := fixture.New(t, fixture.Spec{Modules: specs})

	start := time.Now()
	g, err := BuildGraph(fx.Root)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	build := time.Since(start)
	t.Logf("BuildGraph for 50 modules: %v", build)

	start = time.Now()
	affected := g.Affected([]string{"example.com/mono/core"})
	walk := time.Since(start)
	t.Logf("Affected walk: %v (result size: %d)", walk, len(affected))

	if len(affected) != 50 {
		t.Errorf("expected 50 affected modules, got %d", len(affected))
	}
	if build > 10*time.Second {
		t.Errorf("BuildGraph took %v; want <10s at 50 modules", build)
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

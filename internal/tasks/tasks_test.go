package tasks

import (
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func TestRun_happyPath_allModulesSucceed(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "auth"},
		},
	})
	ws, _ := workspace.Load(fx.Root)

	modules := []string{"example.com/mono/storage", "example.com/mono/auth"}
	results := Run(ws, modules, []string{"go", "test", "./..."})

	if len(results) != 2 {
		t.Fatalf("expected 2 results; got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s failed: %v\n%s", r.Module, r.Err, r.Output)
		}
	}
}

func TestRun_failurePropagated(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	ws, _ := workspace.Load(fx.Root)

	results := Run(ws, []string{"example.com/mono/storage"},
		[]string{"go", "run", "./nonexistent"})

	if len(results) != 1 {
		t.Fatalf("expected 1 result; got %d", len(results))
	}
	if results[0].Err == nil {
		t.Errorf("expected error for nonexistent package; got nil. Output:\n%s", results[0].Output)
	}
}

func TestRun_skipsUnknownModules(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	ws, _ := workspace.Load(fx.Root)

	results := Run(ws, []string{"example.com/mono/storage", "example.com/mono/ghost"},
		[]string{"go", "test", "./..."})

	if len(results) != 1 {
		t.Fatalf("expected 1 result (ghost skipped); got %d", len(results))
	}
	if !strings.Contains(results[0].Module, "storage") {
		t.Errorf("unexpected module: %s", results[0].Module)
	}
}

func TestAnyFailed(t *testing.T) {
	if AnyFailed(nil) {
		t.Error("nil results: should not report failure")
	}
	ok := []Result{{Module: "a", Err: nil}}
	if AnyFailed(ok) {
		t.Error("ok results: should not report failure")
	}
	mixed := []Result{{Module: "a", Err: nil}, {Module: "b", Err: someErr{}}}
	if !AnyFailed(mixed) {
		t.Error("mixed results: should report failure")
	}
}

type someErr struct{}

func (someErr) Error() string { return "boom" }

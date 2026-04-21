package release

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func setupWithReplace(t *testing.T) *workspace.Workspace {
	t.Helper()
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})
	gitRun(t, fx.Root, "tag", "modules/storage/v0.1.0")
	gitRun(t, fx.Root, "tag", "modules/api/v0.1.0")

	apiMod := filepath.Join(fx.Root, "modules/api/go.mod")
	b := fileRead(t, apiMod)
	fileWrite(t, apiMod, b+"\nreplace example.com/mono/storage => ../storage\n")
	gitRun(t, fx.Root, "add", "-A")
	gitRun(t, fx.Root, "commit", "-m", "wip: add replace")

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return ws
}

func TestPlan_defaultsDirectAffectedToPatch(t *testing.T) {
	ws := setupWithReplace(t)

	var out bytes.Buffer
	plan, err := Plan(ws, Options{Slug: "test"}, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan == nil {
		t.Fatal("expected plan")
	}
	// storage is direct-affected; with no --bump, should default to patch.
	var storage, api *struct{ NewVersion string }
	_ = storage
	_ = api
	for _, e := range plan.Entries {
		switch e.ModulePath {
		case "example.com/mono/storage":
			if e.NewVersion != "v0.1.1" {
				t.Errorf("storage defaulted: got %s, want v0.1.1 (patch)", e.NewVersion)
			}
			if e.Kind != bump.Patch {
				t.Errorf("storage kind = %v, want Patch", e.Kind)
			}
		case "example.com/mono/api":
			if e.NewVersion != "v0.1.1" {
				t.Errorf("api cascade: got %s, want v0.1.1 (patch)", e.NewVersion)
			}
		}
	}
}

func TestPlan_bumpsOverrideDefault(t *testing.T) {
	ws := setupWithReplace(t)

	var out bytes.Buffer
	plan, err := Plan(ws, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	}, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, e := range plan.Entries {
		if e.ModulePath == "example.com/mono/storage" && e.NewVersion != "v0.2.0" {
			t.Errorf("storage NewVersion = %s, want v0.2.0", e.NewVersion)
		}
	}
}

func TestPlan_skipDropsModule(t *testing.T) {
	ws := setupWithReplace(t)

	var out bytes.Buffer
	plan, err := Plan(ws, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Skip},
	}, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan != nil {
		for _, e := range plan.Entries {
			if e.ModulePath == "example.com/mono/storage" {
				t.Errorf("storage should be dropped when Skip; got %+v", e)
			}
		}
	}
}

func TestPlan_noAffectedReturnsNil(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	ws, _ := workspace.Load(fx.Root)

	var out bytes.Buffer
	plan, err := Plan(ws, Options{Slug: "test"}, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan != nil {
		t.Errorf("expected nil plan when no replaces; got %+v", plan)
	}
	if !strings.Contains(out.String(), "nothing to release") {
		t.Errorf("expected 'nothing to release' message; got: %s", out.String())
	}
}

// TestPlan_bumpAddsDirectWithoutReplace covers the "publish a library
// that nothing in-tree consumes" path: no replace directive points at
// the module, but the user asked for it explicitly via --bump. The
// module should be treated as direct-affected.
func TestPlan_bumpAddsDirectWithoutReplace(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	gitRun(t, fx.Root, "tag", "modules/storage/v0.1.0")
	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}

	var out bytes.Buffer
	plan, err := Plan(ws, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	}, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected plan; got nil. stdout: %s", out.String())
	}
	var found bool
	for _, e := range plan.Entries {
		if e.ModulePath == "example.com/mono/storage" {
			found = true
			if e.NewVersion != "v0.2.0" {
				t.Errorf("storage NewVersion = %s, want v0.2.0", e.NewVersion)
			}
			if e.Kind != bump.Minor {
				t.Errorf("storage Kind = %v, want Minor", e.Kind)
			}
		}
	}
	if !found {
		t.Errorf("storage missing from plan entries: %+v", plan.Entries)
	}
}

// TestPlan_skipBumpDoesNotAddDirect ensures that --bump foo=skip on a
// module with no replace does NOT add it to the direct set (Skip means
// "don't release", not "add and then skip").
func TestPlan_skipBumpDoesNotAddDirect(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "storage"}},
	})
	gitRun(t, fx.Root, "tag", "modules/storage/v0.1.0")
	ws, _ := workspace.Load(fx.Root)

	var out bytes.Buffer
	plan, err := Plan(ws, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Skip},
	}, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan != nil {
		t.Errorf("expected nil plan (skip of lone module = nothing to release); got %+v", plan)
	}
}

func TestConfirmProceed(t *testing.T) {
	cases := map[string]bool{
		"y\n":   true,
		"Yes\n": true,
		"n\n":   false,
		"\n":    false,
		"nope":  false,
	}
	for in, want := range cases {
		var out bytes.Buffer
		got, err := ConfirmProceed(bufio.NewReader(strings.NewReader(in)), &out)
		if err != nil && in != "" {
			t.Errorf("ConfirmProceed(%q) err=%v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ConfirmProceed(%q) = %v, want %v", in, got, want)
		}
	}
}

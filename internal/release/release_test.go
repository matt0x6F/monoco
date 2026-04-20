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

// fakePrompter records calls and returns scripted kinds.
type fakePrompter struct {
	answers map[string]bump.Kind
	calls   []string
}

func (p *fakePrompter) Ask(mp, cur string, direct bool) (bump.Kind, error) {
	p.calls = append(p.calls, mp)
	if k, ok := p.answers[mp]; ok {
		return k, nil
	}
	return bump.Patch, nil
}

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

	// Add workspace-local replace so storage becomes directly affected.
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

func TestPlan_promptsOnlyForDirectByDefault(t *testing.T) {
	ws := setupWithReplace(t)

	fp := &fakePrompter{answers: map[string]bump.Kind{"example.com/mono/storage": bump.Minor}}
	var out bytes.Buffer
	plan, err := Plan(ws, Options{Slug: "test"}, fp, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan == nil {
		t.Fatal("expected plan")
	}
	// Only storage (direct) should have been prompted; api (cascade) skipped.
	if len(fp.calls) != 1 || fp.calls[0] != "example.com/mono/storage" {
		t.Errorf("expected prompt only for storage; got calls=%v", fp.calls)
	}
}

func TestPlan_promptCascadeAsksForAll(t *testing.T) {
	ws := setupWithReplace(t)

	fp := &fakePrompter{answers: map[string]bump.Kind{
		"example.com/mono/storage": bump.Minor,
		"example.com/mono/api":     bump.Patch,
	}}
	var out bytes.Buffer
	_, err := Plan(ws, Options{Slug: "test", PromptCascade: true}, fp, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(fp.calls) != 2 {
		t.Errorf("expected 2 prompts with PromptCascade; got %v", fp.calls)
	}
}

func TestPlan_failsClosedWithNoPrompter(t *testing.T) {
	ws := setupWithReplace(t)

	var out bytes.Buffer
	_, err := Plan(ws, Options{Slug: "test"}, nil, &out)
	if err == nil {
		t.Fatal("expected error when missing bump + no prompter")
	}
	if !strings.Contains(err.Error(), "supply --bump") {
		t.Errorf("error should hint at --bump; got %v", err)
	}
}

func TestPlan_prefersProvidedBumpsOverPrompts(t *testing.T) {
	ws := setupWithReplace(t)

	fp := &fakePrompter{}
	var out bytes.Buffer
	plan, err := Plan(ws, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/storage": bump.Minor},
	}, fp, &out)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(fp.calls) != 0 {
		t.Errorf("expected no prompts when bumps pre-supplied; got %v", fp.calls)
	}
	// storage bumped minor → v0.2.0.
	for _, e := range plan.Entries {
		if e.ModulePath == "example.com/mono/storage" && e.NewVersion != "v0.2.0" {
			t.Errorf("storage NewVersion = %s, want v0.2.0", e.NewVersion)
		}
	}
}

func TestStdioPrompter_parsesInput(t *testing.T) {
	var out bytes.Buffer
	p := &StdioPrompter{
		In:  bufio.NewReader(strings.NewReader("garbage\nminor\n")),
		Out: &out,
	}
	k, err := p.Ask("foo", "v1.0.0", true)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if k != bump.Minor {
		t.Errorf("got %v, want Minor", k)
	}
	if !strings.Contains(out.String(), "invalid") {
		t.Errorf("expected 'invalid' reprompt in output; got %q", out.String())
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

package propagate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/fixture"
	"github.com/matt0x6f/monoco/internal/workspace"
)

// cycleFixture builds a repo with a module-level require cycle that is
// package-acyclic (the only kind that can exist in compiling Go code):
//
//	users  → imports example.com/mono/auth/base (leaf subpackage)
//	auth   → imports example.com/mono/users
//
// Both go.mods require the other at v0.1.0, tagged. Scenarios then layer
// "new" content on top:
//
//	"one-viable":  auth's new code calls users.NewThing() (new API), so
//	               only users-first stages; users still compiles against
//	               old auth.
//	"coupled":     both new sides call the other's new API; no staged
//	               order compiles.
//	"both-viable": neither new side needs the other's new API.
func cycleFixture(t *testing.T, scenario string) (*fixture.Fixture, *workspace.Workspace) {
	t.Helper()
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{{Name: "auth"}, {Name: "users"}},
	})
	authDir := filepath.Join(fx.Root, "modules/auth")
	usersDir := filepath.Join(fx.Root, "modules/users")

	// v0.1.0 content: the mutual require, package-acyclic.
	writeFile(t, filepath.Join(authDir, "go.mod"),
		"module example.com/mono/auth\n\ngo 1.22\n\nrequire example.com/mono/users v0.1.0\n")
	writeFile(t, filepath.Join(authDir, "auth.go"),
		"package auth\n\nimport \"example.com/mono/users\"\n\nfunc Hello() string { return \"a:\" + users.Hello() }\n")
	if err := os.MkdirAll(filepath.Join(authDir, "base"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(authDir, "base", "base.go"),
		"package base\n\nfunc Base() string { return \"base\" }\n")
	writeFile(t, filepath.Join(usersDir, "go.mod"),
		"module example.com/mono/users\n\ngo 1.22\n\nrequire example.com/mono/auth v0.1.0\n")
	writeFile(t, filepath.Join(usersDir, "users.go"),
		"package users\n\nimport \"example.com/mono/auth/base\"\n\nfunc Hello() string { return \"u:\" + base.Base() }\n")
	// The fixture's generated tests reference functions we replaced.
	os.Remove(filepath.Join(authDir, "auth_test.go"))
	os.Remove(filepath.Join(usersDir, "users_test.go"))
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "cycle fixture v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/auth/v0.1.0")
	run(t, fx.Root, "git", "tag", "modules/users/v0.1.0")
	run(t, fx.Root, "git", "push", "origin", "main")
	run(t, fx.Root, "git", "push", "origin", "--tags")

	// New content per scenario, plus the workspace-local replace that
	// marks auth as in-flight.
	switch scenario {
	case "one-viable":
		writeFile(t, filepath.Join(usersDir, "users.go"),
			"package users\n\nimport \"example.com/mono/auth/base\"\n\nfunc Hello() string { return \"u:\" + base.Base() }\nfunc NewThing() string { return \"new\" }\n")
		writeFile(t, filepath.Join(authDir, "auth.go"),
			"package auth\n\nimport \"example.com/mono/users\"\n\nfunc Hello() string { return \"a:\" + users.NewThing() }\n")
	case "coupled":
		writeFile(t, filepath.Join(authDir, "base", "base.go"),
			"package base\n\nfunc Base() string { return \"base\" }\nfunc NewBase() string { return \"nb\" }\n")
		writeFile(t, filepath.Join(usersDir, "users.go"),
			"package users\n\nimport \"example.com/mono/auth/base\"\n\nfunc Hello() string { return \"u:\" + base.NewBase() }\nfunc NewThing() string { return \"new\" }\n")
		writeFile(t, filepath.Join(authDir, "auth.go"),
			"package auth\n\nimport \"example.com/mono/users\"\n\nfunc Hello() string { return \"a:\" + users.NewThing() }\n")
	case "both-viable":
		writeFile(t, filepath.Join(usersDir, "users.go"),
			"package users\n\nimport \"example.com/mono/auth/base\"\n\nfunc Hello() string { return \"u:\" + base.Base() }\nfunc NewThing() string { return \"new\" }\n")
		writeFile(t, filepath.Join(authDir, "auth.go"),
			"package auth\n\nimport \"example.com/mono/users\"\n\nfunc Hello() string { return \"a:\" + users.Hello() }\nfunc Feature() string { return \"f\" }\n")
	default:
		t.Fatalf("unknown scenario %q", scenario)
	}
	old, err := osReadFile(filepath.Join(usersDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(filepath.Join(usersDir, "go.mod"),
		old+"\nreplace example.com/mono/auth => ../auth\n"); err != nil {
		t.Fatal(err)
	}
	run(t, fx.Root, "git", "add", "-A")
	run(t, fx.Root, "git", "commit", "-m", "in-flight: "+scenario)

	ws, err := workspace.Load(fx.Root)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}
	return fx, ws
}

func TestStagedPlan_CycleStagesViableDirection(t *testing.T) {
	_, ws := cycleFixture(t, "one-viable")
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/auth"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/auth": bump.Minor},
	})
	if err != nil {
		t.Fatalf("NewPlanForModules: %v", err)
	}
	if plan.Stages != 2 {
		t.Fatalf("Stages = %d, want 2; entries: %+v", plan.Stages, plan.Entries)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("entries = %+v, want users then auth", plan.Entries)
	}
	users, auth := plan.Entries[0], plan.Entries[1]
	if users.ModulePath != "example.com/mono/users" || users.Stage != 1 {
		t.Errorf("stage 1 should be users (only it compiles against old auth): %+v", plan.Entries)
	}
	if auth.ModulePath != "example.com/mono/auth" || auth.Stage != 2 {
		t.Errorf("stage 2 should be auth: %+v", plan.Entries)
	}
	if len(users.PinnedOld) != 1 || users.PinnedOld[0] != (Pin{Path: "example.com/mono/auth", Version: "v0.1.0"}) {
		t.Errorf("users should pin auth@v0.1.0 (previous), got %+v", users.PinnedOld)
	}
	if len(auth.PinnedOld) != 0 {
		t.Errorf("auth releases last and pins nothing, got %+v", auth.PinnedOld)
	}
	if auth.NewVersion != "v0.2.0" || users.NewVersion != "v0.1.1" {
		t.Errorf("versions: auth %s (want v0.2.0), users %s (want v0.1.1)", auth.NewVersion, users.NewVersion)
	}
}

func TestStagedPlan_ReleaseCoupled(t *testing.T) {
	_, ws := cycleFixture(t, "coupled")
	_, err := NewPlanForModules(ws, []string{"example.com/mono/auth"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/auth": bump.Minor},
	})
	if err == nil {
		t.Fatal("expected release-coupled error, got nil")
	}
	if !strings.Contains(err.Error(), "release-coupled") {
		t.Errorf("error should say release-coupled, got: %v", err)
	}
	for _, m := range []string{"example.com/mono/auth", "example.com/mono/users"} {
		if !strings.Contains(err.Error(), m) {
			t.Errorf("error should name %s, got: %v", m, err)
		}
	}
}

func TestStagedPlan_CutOverride(t *testing.T) {
	_, ws := cycleFixture(t, "both-viable")

	// Default: cascaded member (users) peels first so the actively
	// developed module ships last.
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/auth"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/auth": bump.Minor},
	})
	if err != nil {
		t.Fatalf("default plan: %v", err)
	}
	if plan.Entries[0].ModulePath != "example.com/mono/users" {
		t.Errorf("default cut should stage users first, got %+v", plan.Entries)
	}

	// --cut auth forces auth to release first, pinned to old users.
	plan, err = NewPlanForModules(ws, []string{"example.com/mono/auth"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/auth": bump.Minor},
		Cuts:  map[string]struct{}{"example.com/mono/auth": {}},
	})
	if err != nil {
		t.Fatalf("cut plan: %v", err)
	}
	auth, users := plan.Entries[0], plan.Entries[1]
	if auth.ModulePath != "example.com/mono/auth" || auth.Stage != 1 {
		t.Fatalf("--cut auth should stage auth first, got %+v", plan.Entries)
	}
	if len(auth.PinnedOld) != 1 || auth.PinnedOld[0] != (Pin{Path: "example.com/mono/users", Version: "v0.1.0"}) {
		t.Errorf("auth should pin users@v0.1.0, got %+v", auth.PinnedOld)
	}
	if users.Stage != 2 || len(users.PinnedOld) != 0 {
		t.Errorf("users should release second pinning nothing, got %+v", users)
	}

	// --cut on a module not in a cycle is refused.
	_, err = NewPlanForModules(ws, []string{"example.com/mono/auth"}, Options{
		Slug: "test",
		Bumps: map[string]bump.Kind{
			"example.com/mono/auth":  bump.Minor,
			"example.com/mono/users": bump.Skip, // skip breaks the cycle
		},
		Cuts: map[string]struct{}{"example.com/mono/auth": {}},
	})
	if err == nil || !strings.Contains(err.Error(), "not part of a require cycle") {
		t.Errorf("--cut without a cycle should be refused, got: %v", err)
	}
}

func TestApply_StagedRelease(t *testing.T) {
	fx, ws := cycleFixture(t, "one-viable")
	plan, err := NewPlanForModules(ws, []string{"example.com/mono/auth"}, Options{
		Slug:  "test",
		Bumps: map[string]bump.Kind{"example.com/mono/auth": bump.Minor},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	res, err := Apply(ws, plan, ApplyOptions{Remote: "origin"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(res.StageCommits) != 2 || res.StageCommits[0] == res.StageCommits[1] {
		t.Fatalf("want 2 distinct stage commits, got %v", res.StageCommits)
	}
	if res.ReleaseCommit != res.StageCommits[1] {
		t.Errorf("ReleaseCommit should be the chain tip: %v vs %v", res.ReleaseCommit, res.StageCommits)
	}

	// Each tag points at its own stage's commit; the train marks the tip.
	for tag, want := range map[string]string{
		"modules/users/v0.1.1": res.StageCommits[0],
		"modules/auth/v0.2.0":  res.StageCommits[1],
		plan.TrainTag:          res.StageCommits[1],
	} {
		sha := strings.TrimSpace(gitOut(t, fx.Root, "rev-list", "-n", "1", tag))
		if sha != want {
			t.Errorf("tag %s at %s, want %s", tag, sha, want)
		}
	}

	// Stage messages are suffixed.
	log := gitOut(t, fx.Root, "log", "--format=%s", "-n", "2")
	if !strings.Contains(log, "(stage 2/2)") || !strings.Contains(log, "(stage 1/2)") {
		t.Errorf("expected stage-suffixed commit messages, got:\n%s", log)
	}

	// The back-edge: users ships still requiring auth's PREVIOUS tag,
	// with the in-flight replace stripped.
	usersGoMod, err := osReadFile(filepath.Join(fx.Root, "modules/users/go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(usersGoMod, "require example.com/mono/auth v0.1.0") {
		t.Errorf("users must keep requiring auth@v0.1.0:\n%s", usersGoMod)
	}
	if strings.Contains(usersGoMod, "replace") {
		t.Errorf("in-flight replace should be stripped:\n%s", usersGoMod)
	}
	// users must NOT pin auth's new version anywhere.
	if sum, err := osReadFile(filepath.Join(fx.Root, "modules/users/go.sum")); err == nil &&
		strings.Contains(sum, "auth v0.2.0") {
		t.Errorf("users/go.sum must not reference auth's new version:\n%s", sum)
	}

	// The forward edge: auth pins users' new version, and the hash it
	// pins is the canonical hash of users' post-rewrite (tagged) content.
	authGoMod, err := osReadFile(filepath.Join(fx.Root, "modules/auth/go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authGoMod, "require example.com/mono/users v0.1.1") {
		t.Errorf("auth must require users@v0.1.1:\n%s", authGoMod)
	}
	want, err := ComputeModuleHashes(filepath.Join(fx.Root, "modules/users"), "example.com/mono/users", "v0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	authSum, err := osReadFile(filepath.Join(fx.Root, "modules/auth/go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authSum, "example.com/mono/users v0.1.1 "+want.H1) ||
		!strings.Contains(authSum, "example.com/mono/users v0.1.1/go.mod "+want.H1Mod) {
		t.Errorf("auth/go.sum must pin users' post-rewrite hashes:\nwant %s / %s\ngot:\n%s", want.H1, want.H1Mod, authSum)
	}

	if !res.Pushed {
		t.Error("expected atomic push to the fixture remote")
	}
}

func gitOut(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

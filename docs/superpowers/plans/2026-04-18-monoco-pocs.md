# monoco — Proof-of-Concept Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** De-risk three technical bets underlying the monoco design before committing to a full v1 implementation. Each POC is a standalone Go program that answers one concrete question against a disposable fixture monorepo.

**Architecture:** Three independent POCs sharing a common fixture-repo generator. Each POC is a `main.go` under `pocs/<name>/` that exits 0 on success, prints findings on exit. Findings roll up into a single `pocs/FINDINGS.md` that informs the v1 plan.

**Tech Stack:** Go 1.22+, `golang.org/x/mod` (modfile + semver), `os/exec` for shelling out to `go` and `git`. No third-party test framework — stdlib `testing`.

**Design spec:** [docs/superpowers/specs](../specs/) → see the approved design at [/Users/matt/.claude/plans/i-want-you-to-polymorphic-mccarthy.md](/Users/matt/.claude/plans/i-want-you-to-polymorphic-mccarthy.md).

---

## Technical Bets Under Test

| # | Bet | POC | Kill criterion |
|---|-----|-----|----------------|
| 1 | `go list -m -json all` + `go.work` gives us enough data to compute transitive reverse-dep affected sets without parsing Go source. | POC-1 | Data is missing required fields OR graph walk takes >1s on a 50-module fixture. |
| 2 | We can verify a rewritten `go.mod` is self-consistent **before** any tag exists on the remote, so `apply` never pushes a broken tag. | POC-2 | No verification approach works without pre-existing tags, OR the viable approach requires mutating shared state (dirty working tree after verify). |
| 3 | `git push --atomic` with N module tags + 1 train tag + 1 branch update lands all-or-nothing, including under adversarial server-side hook rejection. | POC-3 | Push is not actually atomic (partial tags land on remote after a rejection). |

## File Structure

```
/Users/matt/Developer/monoco/
├── .gitignore
├── go.mod                                  # module: github.com/<owner>/monoco  (set in Task 0)
├── docs/superpowers/plans/
│   └── 2026-04-18-monoco-pocs.md           # this file
├── internal/
│   └── fixture/
│       ├── fixture.go                      # shared fixture-repo generator
│       └── fixture_test.go
├── pocs/
│   ├── FINDINGS.md                         # populated as POCs complete
│   ├── 01-revdep-graph/
│   │   ├── main.go
│   │   └── main_test.go
│   ├── 02-gomod-verify/
│   │   ├── main.go
│   │   └── main_test.go
│   └── 03-atomic-push/
│       ├── main.go
│       └── main_test.go
```

### Responsibilities

- `internal/fixture` — produces a disposable on-disk monorepo under a `t.TempDir()` with N modules in declared dep relationships, a `go.work`, initialized git repo + bare remote. Shared by all POCs so we exercise the same shape consistently.
- `pocs/01-revdep-graph` — answers bet 1: given a fixture, can we compute the affected set of a touched module?
- `pocs/02-gomod-verify` — answers bet 2: given rewritten go.mods, can we verify consistency without pre-existing remote tags?
- `pocs/03-atomic-push` — answers bet 3: does `git push --atomic` behave as advertised?

Each POC has a `main.go` (for running manually / reading the report) and a `main_test.go` (for automated success criteria).

---

## Task 0: Repository scaffold

**Files:**
- Create: `/Users/matt/Developer/monoco/.gitignore`
- Create: `/Users/matt/Developer/monoco/go.mod`
- Create: `/Users/matt/Developer/monoco/README.md`

This task turns the empty directory into a Go module and git repository so subsequent tasks have somewhere to commit.

- [ ] **Step 1: Initialize git repo**

Run:
```bash
cd /Users/matt/Developer/monoco && git init && git branch -M main
```
Expected: `Initialized empty Git repository in /Users/matt/Developer/monoco/.git/`

- [ ] **Step 2: Write `.gitignore`**

Create `/Users/matt/Developer/monoco/.gitignore`:
```
# Go
*.exe
*.test
*.out
/bin/
/dist/

# IDEs
.idea/
.vscode/
*.swp

# Local env
.env
.env.local

# POC scratch
pocs/**/tmp/
pocs/**/*.log

# Already present — preserve
.remember/logs/
.remember/tmp/
```

- [ ] **Step 3: Write `go.mod`**

Create `/Users/matt/Developer/monoco/go.mod`:
```
module github.com/matt-ouille/monoco

go 1.22
```

(Replace the module path if the user prefers a different owner. Ask if uncertain.)

- [ ] **Step 4: Write a minimal `README.md`**

Create `/Users/matt/Developer/monoco/README.md`:
```markdown
# monoco

Go-native monorepo tooling: atomic propagation of changes across module boundaries via `go.work` + coordinated git tags.

See [design spec](docs/superpowers/specs/) and [POC plan](docs/superpowers/plans/2026-04-18-monoco-pocs.md).

Status: pre-alpha. Proving out technical bets.
```

- [ ] **Step 5: Initial commit**

Run:
```bash
cd /Users/matt/Developer/monoco && git add -A && git commit -m "chore: initial scaffold"
```
Expected: commit succeeds; `git status` reports clean tree.

---

## Task 1: Shared fixture generator

**Files:**
- Create: `/Users/matt/Developer/monoco/internal/fixture/fixture.go`
- Create: `/Users/matt/Developer/monoco/internal/fixture/fixture_test.go`

The fixture generator produces a throwaway on-disk monorepo. Each POC uses it to set up a known shape.

Shape per fixture call:
- Three modules under `modules/` named by the caller, with declared dependency relationships passed in a DAG description.
- Each module has `go.mod` (module path = `example.com/mono/<name>`), one `.go` file with an exported function, and a test for that function.
- A `go.work` at the root listing all modules.
- A git repo initialized at the root, with one initial commit.
- A `file://` bare remote created in a sibling temp dir; root repo has `origin` set to it.

- [ ] **Step 1: Write the test**

Create `/Users/matt/Developer/monoco/internal/fixture/fixture_test.go`:
```go
package fixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewMonorepo_basicShape(t *testing.T) {
	fx := New(t, Spec{
		Modules: []ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
			{Name: "auth"},
		},
	})

	// go.work exists and lists all three modules.
	workBytes, err := os.ReadFile(filepath.Join(fx.Root, "go.work"))
	if err != nil {
		t.Fatalf("read go.work: %v", err)
	}
	for _, name := range []string{"storage", "api", "auth"} {
		if !containsLine(string(workBytes), "\t./modules/"+name) {
			t.Errorf("go.work missing entry for %s; contents:\n%s", name, workBytes)
		}
	}

	// Each module's go.mod exists.
	for _, name := range []string{"storage", "api", "auth"} {
		p := filepath.Join(fx.Root, "modules", name, "go.mod")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}

	// api's go.mod requires storage.
	apiMod, err := os.ReadFile(filepath.Join(fx.Root, "modules", "api", "go.mod"))
	if err != nil {
		t.Fatalf("read api go.mod: %v", err)
	}
	if !containsLine(string(apiMod), "require example.com/mono/storage v0.0.0-00010101000000-000000000000") {
		t.Errorf("api/go.mod missing require for storage; contents:\n%s", apiMod)
	}

	// Git repo initialized with one commit.
	cmd := exec.Command("git", "-C", fx.Root, "rev-list", "--count", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-list: %v: %s", err, out)
	}
	if got := string(out); got != "1\n" {
		t.Errorf("expected 1 commit; got %q", got)
	}

	// Origin is set to the bare remote and is reachable.
	cmd = exec.Command("git", "-C", fx.Root, "remote", "get-url", "origin")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("get-url: %v: %s", err, out)
	}
	if len(out) == 0 {
		t.Fatal("origin URL empty")
	}
}

func containsLine(haystack, needle string) bool {
	for _, line := range splitLines(haystack) {
		if line == needle {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./internal/fixture/...
```
Expected: FAIL with `undefined: New` or `undefined: Spec`.

- [ ] **Step 3: Implement `fixture.go`**

Create `/Users/matt/Developer/monoco/internal/fixture/fixture.go`:
```go
// Package fixture produces disposable on-disk monorepos for POCs and tests.
package fixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ModuleSpec declares one module in a fixture.
type ModuleSpec struct {
	Name      string   // short name; module path becomes example.com/mono/<name>
	DependsOn []string // names of modules this one requires
}

// Spec describes a fixture to be created.
type Spec struct {
	Modules []ModuleSpec
}

// Fixture is a materialized on-disk monorepo. Cleaned up automatically via t.Cleanup.
type Fixture struct {
	Root      string // working tree root
	RemoteDir string // bare remote directory (file://)
}

// New creates a fixture in a fresh temp dir and returns its paths.
// The git repo is initialized, modules are committed, and origin is wired to a local bare remote.
func New(t *testing.T, spec Spec) *Fixture {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")

	// Bare remote.
	mustRun(t, "", "git", "init", "--bare", remote)

	// Working tree.
	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "checkout", "-b", "main")
	mustRun(t, root, "git", "config", "user.email", "fixture@monoco.test")
	mustRun(t, root, "git", "config", "user.name", "fixture")
	mustRun(t, root, "git", "remote", "add", "origin", remote)

	// Modules.
	for _, m := range spec.Modules {
		writeModule(t, root, m)
	}
	writeWorkspace(t, root, spec)

	// Initial commit.
	mustRun(t, root, "git", "add", "-A")
	mustRun(t, root, "git", "commit", "-m", "initial fixture")

	return &Fixture{Root: root, RemoteDir: remote}
}

func writeModule(t *testing.T, root string, m ModuleSpec) {
	t.Helper()
	dir := filepath.Join(root, "modules", m.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	// go.mod
	var b strings.Builder
	fmt.Fprintf(&b, "module example.com/mono/%s\n\ngo 1.22\n", m.Name)
	for _, dep := range m.DependsOn {
		// Pseudo-version placeholder; workspace mode will resolve to local path.
		fmt.Fprintf(&b, "\nrequire example.com/mono/%s v0.0.0-00010101000000-000000000000\n", dep)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), b.String())

	// Source file with one exported function.
	funcName := strings.ToUpper(m.Name[:1]) + m.Name[1:] + "Hello"
	var src strings.Builder
	fmt.Fprintf(&src, "package %s\n\n", m.Name)
	for _, dep := range m.DependsOn {
		fmt.Fprintf(&src, "import \"example.com/mono/%s\"\n\n", dep)
	}
	fmt.Fprintf(&src, "func %s() string {\n", funcName)
	if len(m.DependsOn) == 0 {
		fmt.Fprintf(&src, "\treturn %q\n", m.Name)
	} else {
		dep := m.DependsOn[0]
		depFunc := strings.ToUpper(dep[:1]) + dep[1:] + "Hello"
		fmt.Fprintf(&src, "\treturn %q + \" \" + %s.%s()\n", m.Name, dep, depFunc)
	}
	fmt.Fprintf(&src, "}\n")
	writeFile(t, filepath.Join(dir, m.Name+".go"), src.String())

	// Test.
	var test strings.Builder
	fmt.Fprintf(&test, "package %s\n\nimport \"testing\"\n\n", m.Name)
	fmt.Fprintf(&test, "func Test%s(t *testing.T) {\n", funcName)
	fmt.Fprintf(&test, "\tif %s() == \"\" {\n\t\tt.Fatal(\"empty\")\n\t}\n}\n", funcName)
	writeFile(t, filepath.Join(dir, m.Name+"_test.go"), test.String())
}

func writeWorkspace(t *testing.T, root string, spec Spec) {
	t.Helper()
	var b strings.Builder
	b.WriteString("go 1.22\n\nuse (\n")
	for _, m := range spec.Modules {
		fmt.Fprintf(&b, "\t./modules/%s\n", m.Name)
	}
	b.WriteString(")\n")
	writeFile(t, filepath.Join(root, "go.work"), b.String())
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./internal/fixture/...
```
Expected: `ok  github.com/matt-ouille/monoco/internal/fixture`

- [ ] **Step 5: Commit**

```bash
cd /Users/matt/Developer/monoco && git add -A && git commit -m "feat(fixture): shared monorepo fixture generator"
```

---

## Task 2: POC-1 — reverse-dep graph from `go list`

**Files:**
- Create: `/Users/matt/Developer/monoco/pocs/01-revdep-graph/main.go`
- Create: `/Users/matt/Developer/monoco/pocs/01-revdep-graph/main_test.go`

**Question being answered:** given a fixture monorepo, can we compute the affected set of a touched module using only `go.work` + `go list -m -json all`, without parsing Go source?

**Success criteria (test-encoded):**
- Touching `storage` returns affected set `{storage, api}` (api depends on storage; auth does not).
- Touching `auth` returns `{auth}`.
- Graph build + walk completes in <1s on the three-module fixture. (We'll re-measure on a 50-module fixture in Task 5.)

- [ ] **Step 1: Write the failing test**

Create `/Users/matt/Developer/monoco/pocs/01-revdep-graph/main_test.go`:
```go
package main

import (
	"sort"
	"testing"
	"time"

	"github.com/matt-ouille/monoco/internal/fixture"
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./pocs/01-revdep-graph/...
```
Expected: FAIL with `undefined: BuildGraph`.

- [ ] **Step 3: Implement `main.go`**

Create `/Users/matt/Developer/monoco/pocs/01-revdep-graph/main.go`:
```go
// POC-1: compute affected module sets from go.work + `go list -m -json all`.
//
// Answers: can we build a reverse-dependency graph across a Go monorepo
// without parsing source, and compute transitive affected sets fast enough?
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// Graph is a module-path -> set-of-module-paths reverse-dep map
// restricted to modules inside the workspace.
type Graph struct {
	// WorkspaceModules maps module path -> on-disk dir.
	WorkspaceModules map[string]string
	// ReverseDeps[A] = set of modules in the workspace that require A.
	ReverseDeps map[string]map[string]struct{}
}

// BuildGraph parses <root>/go.work and runs `go list -m -json all` in each
// module to extract dependency edges, keeping only edges where both endpoints
// are workspace modules.
func BuildGraph(root string) (*Graph, error) {
	workPath := filepath.Join(root, "go.work")
	workBytes, err := os.ReadFile(workPath)
	if err != nil {
		return nil, fmt.Errorf("read go.work: %w", err)
	}
	wf, err := modfile.ParseWork(workPath, workBytes, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.work: %w", err)
	}

	g := &Graph{
		WorkspaceModules: map[string]string{},
		ReverseDeps:      map[string]map[string]struct{}{},
	}

	// Resolve module paths from each workspace entry by reading its go.mod.
	type entry struct{ path, dir string }
	var entries []entry
	for _, u := range wf.Use {
		modDir := filepath.Join(root, u.Path)
		gm, err := os.ReadFile(filepath.Join(modDir, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("read %s/go.mod: %w", modDir, err)
		}
		mf, err := modfile.Parse(filepath.Join(modDir, "go.mod"), gm, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s/go.mod: %w", modDir, err)
		}
		g.WorkspaceModules[mf.Module.Mod.Path] = modDir
		entries = append(entries, entry{path: mf.Module.Mod.Path, dir: modDir})
	}

	// For each workspace module, list its dependencies and record edges
	// whose targets are also workspace modules.
	for _, e := range entries {
		deps, err := listDirectDeps(e.dir)
		if err != nil {
			return nil, fmt.Errorf("list deps for %s: %w", e.path, err)
		}
		for _, dep := range deps {
			if _, ok := g.WorkspaceModules[dep]; !ok {
				continue
			}
			if g.ReverseDeps[dep] == nil {
				g.ReverseDeps[dep] = map[string]struct{}{}
			}
			g.ReverseDeps[dep][e.path] = struct{}{}
		}
	}

	return g, nil
}

// listDirectDeps runs `go list -m -json all` in moduleDir and returns the
// set of modules it requires directly or indirectly.
func listDirectDeps(moduleDir string) ([]string, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = moduleDir
	// Disable workspace so we see this module's go.mod view, not the union.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr.String())
	}

	type mod struct {
		Path string
		Main bool
	}
	var deps []string
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var m mod
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		if m.Main {
			continue
		}
		deps = append(deps, m.Path)
	}
	return deps, nil
}

// Affected returns the transitive closure of touched modules under ReverseDeps,
// including the touched modules themselves.
func (g *Graph) Affected(touched []string) []string {
	seen := map[string]struct{}{}
	var stack []string
	for _, t := range touched {
		if _, ok := g.WorkspaceModules[t]; !ok {
			continue
		}
		seen[t] = struct{}{}
		stack = append(stack, t)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for consumer := range g.ReverseDeps[cur] {
			if _, ok := seen[consumer]; ok {
				continue
			}
			seen[consumer] = struct{}{}
			stack = append(stack, consumer)
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: 01-revdep-graph <repo-root> <touched-module-path> [<touched-module-path>...]")
		os.Exit(2)
	}
	root := os.Args[1]
	touched := os.Args[2:]
	g, err := BuildGraph(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	affected := g.Affected(touched)
	fmt.Println(strings.Join(affected, "\n"))
}
```

- [ ] **Step 4: Add `golang.org/x/mod` as a dependency**

Run:
```bash
cd /Users/matt/Developer/monoco && go get golang.org/x/mod@latest && go mod tidy
```

- [ ] **Step 5: Run the test to verify it passes**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./pocs/01-revdep-graph/... -v
```
Expected: `--- PASS: TestAffectedSet_simpleFixture`. If any case fails, write the actual vs. expected diff to `pocs/FINDINGS.md` and stop for a design revisit — this is a kill criterion.

- [ ] **Step 6: Commit**

```bash
cd /Users/matt/Developer/monoco && git add -A && git commit -m "feat(poc-1): reverse-dep graph from go list + go.work"
```

---

## Task 3: POC-2 — go.mod consistency verification without remote tags

**Files:**
- Create: `/Users/matt/Developer/monoco/pocs/02-gomod-verify/main.go`
- Create: `/Users/matt/Developer/monoco/pocs/02-gomod-verify/main_test.go`

**Question being answered:** after rewriting `api/go.mod` to require a new version of `storage` that has not yet been tagged, can we verify the rewritten `go.mod` is self-consistent — without mutating shared state and without relying on a remote tag existing?

**Approach under test:** create *local* git tags first (`storage/v0.9.0` on the current commit), run `GOWORK=off GOPROXY=off GOFLAGS=-mod=mod go build ./...` in the affected module, expect success. The idea: local tags are visible to `go` via the local module cache, and disabling the proxy forces direct VCS resolution, which sees the local tag.

Alternate approach we may fall back to: insert temporary `replace => ./relative/path` directives pointing to the in-tree module, run `go build`, then strip the replaces. Cheaper but leaves a window where go.mod has a replace the committed version doesn't.

**Success criteria (test-encoded):**
- With consistent rewrite (api requires storage@v0.9.0, local tag exists at a commit with matching go.mod content): build succeeds.
- With *inconsistent* rewrite (api requires storage@v0.9.0 but local tag doesn't exist or points at an incompatible commit): build fails and the error message mentions the missing version.
- No files under `fx.Root` are modified after `Verify` returns (verified by comparing a git status snapshot).

- [ ] **Step 1: Write the failing test**

Create `/Users/matt/Developer/monoco/pocs/02-gomod-verify/main_test.go`:
```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt-ouille/monoco/internal/fixture"
)

func TestVerify_consistent(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})

	// Simulate an apply: rewrite api/go.mod to require storage@v0.9.0,
	// commit the rewrite, then tag storage/v0.9.0 locally.
	rewriteRequire(t, filepath.Join(fx.Root, "modules/api/go.mod"),
		"example.com/mono/storage", "v0.9.0")
	commitAll(t, fx.Root, "release: train/test")
	tag(t, fx.Root, "modules/storage/v0.9.0", "HEAD")
	// Note: the go tool expects module tags in the form <module-path>/v<X.Y.Z>
	// where module-path is relative to the repo root, not the full go-module path.
	// We use the filesystem sub-path, which matches Go's convention for nested modules.

	snapshot := gitStatus(t, fx.Root)

	if err := Verify(fx.Root, []string{"example.com/mono/api"}); err != nil {
		t.Fatalf("Verify on consistent rewrite: %v", err)
	}

	if got := gitStatus(t, fx.Root); got != snapshot {
		t.Errorf("Verify mutated working tree:\nbefore:\n%s\nafter:\n%s", snapshot, got)
	}
}

func TestVerify_inconsistent_missingTag(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})

	// Rewrite api/go.mod to require a version that has NO tag.
	rewriteRequire(t, filepath.Join(fx.Root, "modules/api/go.mod"),
		"example.com/mono/storage", "v0.9.0")
	commitAll(t, fx.Root, "release: train/test")
	// Do NOT tag storage/v0.9.0.

	err := Verify(fx.Root, []string{"example.com/mono/api"})
	if err == nil {
		t.Fatal("Verify succeeded on inconsistent rewrite; expected failure")
	}
	if !strings.Contains(err.Error(), "v0.9.0") {
		t.Errorf("error should mention missing version; got: %v", err)
	}
}

func rewriteRequire(t *testing.T, goModPath, depPath, newVersion string) {
	t.Helper()
	b, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read %s: %v", goModPath, err)
	}
	lines := strings.Split(string(b), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "require "+depPath+" ") {
			lines[i] = "require " + depPath + " " + newVersion
		}
	}
	if err := os.WriteFile(goModPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write %s: %v", goModPath, err)
	}
}

func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-m", msg)
}

func tag(t *testing.T, root, name, ref string) {
	t.Helper()
	run(t, root, "git", "tag", name, ref)
}

func gitStatus(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	return string(out)
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./pocs/02-gomod-verify/...
```
Expected: FAIL with `undefined: Verify`.

- [ ] **Step 3: Implement `main.go` with the primary approach (local tags + GOPROXY=off)**

Create `/Users/matt/Developer/monoco/pocs/02-gomod-verify/main.go`:
```go
// POC-2: verify rewritten go.mod files are self-consistent BEFORE publishing
// tags to a remote. Uses local git tags + GOPROXY=off,direct so `go build`
// resolves versions directly from the local repo.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Verify attempts a module-mode build of each named module under root.
// It does not mutate the working tree.
func Verify(root string, moduleDirs []string) error {
	for _, mod := range moduleDirs {
		// Resolve the on-disk directory for the module by scanning go.work.
		// For the POC we accept both module paths and relative dirs — if it
		// looks like a module path, find its dir via go.work.
		dir, err := resolveModuleDir(root, mod)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", mod, err)
		}

		cmd := exec.Command("go", "build", "./...")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GOWORK=off",
			"GOPROXY=off",
			"GOFLAGS=-mod=mod",
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("verify build failed in %s: %w\nstderr: %s", dir, err, stderr.String())
		}
	}
	return nil
}

func resolveModuleDir(root, modulePath string) (string, error) {
	// Simplest POC implementation: read go.work, walk each `use` entry,
	// read its go.mod, match on the module path.
	workBytes, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return "", err
	}
	// Hand-rolled parse is fine for POC; production uses modfile.ParseWork.
	var current string
	for _, line := range splitLines(string(workBytes)) {
		line = trimSpace(line)
		if line == "" || line[0] == '/' || line[0] == '#' {
			continue
		}
		if line[0] == '.' {
			// candidate directory
			candidate := filepath.Join(root, line)
			gm, err := os.ReadFile(filepath.Join(candidate, "go.mod"))
			if err != nil {
				continue
			}
			if declaredPath := moduleDeclaration(string(gm)); declaredPath == modulePath {
				return candidate, nil
			}
			_ = current
		}
	}
	return "", fmt.Errorf("module %s not found in go.work", modulePath)
}

func moduleDeclaration(goMod string) string {
	for _, line := range splitLines(goMod) {
		line = trimSpace(line)
		const prefix = "module "
		if len(line) > len(prefix) && line[:len(prefix)] == prefix {
			return trimSpace(line[len(prefix):])
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: 02-gomod-verify <repo-root> <module-path> [<module-path>...]")
		os.Exit(2)
	}
	if err := Verify(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("OK")
}
```

- [ ] **Step 4: Run the test**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./pocs/02-gomod-verify/... -v
```
Expected: both subtests PASS.

**If `TestVerify_consistent` fails** (most likely failure mode — nested-module tag resolution is subtle):
  1. Capture the exact error in `pocs/FINDINGS.md` under POC-2.
  2. Try the fallback approach: insert temporary `replace <dep> => ../../modules/<dep>` directives, build, restore go.mod from git. Implement as a second code path in `Verify` guarded by a `strategy` parameter.
  3. Re-run. If the fallback passes, document both strategies in FINDINGS and recommend the fallback for v1.
  4. If neither strategy passes, this is a kill criterion — stop and revisit the design with the user.

- [ ] **Step 5: Commit**

```bash
cd /Users/matt/Developer/monoco && git add -A && git commit -m "feat(poc-2): go.mod verification via local tags + GOPROXY=off"
```

---

## Task 4: POC-3 — atomic multi-tag push

**Files:**
- Create: `/Users/matt/Developer/monoco/pocs/03-atomic-push/main.go`
- Create: `/Users/matt/Developer/monoco/pocs/03-atomic-push/main_test.go`

**Question being answered:** does `git push --atomic origin main <tag>...` behave as all-or-nothing even under partial rejection? Specifically:
- When all refs are acceptable: all land.
- When one ref is rejected by a pre-receive hook: *none* land.

**Success criteria (test-encoded):**
- Happy path: push main + 3 tags (2 module tags + 1 train tag). After push, remote has main updated AND all three tags.
- Rejection path: install a pre-receive hook on the remote that rejects any push of a ref matching `refs/tags/train/*`. Attempt the same push. After the failure, remote has none of the three tags AND main is unchanged.

- [ ] **Step 1: Write the failing test**

Create `/Users/matt/Developer/monoco/pocs/03-atomic-push/main_test.go`:
```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matt-ouille/monoco/internal/fixture"
)

func TestAtomicPush_happyPath(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})

	// Create a release commit + tags locally.
	if err := os.WriteFile(filepath.Join(fx.Root, "modules/api/go.mod.note"), []byte("release marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "release: train/test")

	tags := []string{
		"modules/storage/v0.9.0",
		"modules/api/v1.4.3",
		"train/2026-04-18-test",
	}
	for _, tg := range tags {
		runT(t, fx.Root, "git", "tag", tg, "HEAD")
	}

	if err := AtomicPush(fx.Root, "origin", "main", tags); err != nil {
		t.Fatalf("AtomicPush: %v", err)
	}

	remoteTags := listRemoteTags(t, fx.RemoteDir)
	for _, tg := range tags {
		if !contains(remoteTags, tg) {
			t.Errorf("remote missing tag %s; has: %v", tg, remoteTags)
		}
	}
}

func TestAtomicPush_rejectedByHook(t *testing.T) {
	fx := fixture.New(t, fixture.Spec{
		Modules: []fixture.ModuleSpec{
			{Name: "storage"},
			{Name: "api", DependsOn: []string{"storage"}},
		},
	})

	// Install a pre-receive hook on the remote that rejects train tags.
	hookPath := filepath.Join(fx.RemoteDir, "hooks", "pre-receive")
	hookBody := `#!/bin/sh
while read oldrev newrev refname; do
  case "$refname" in
    refs/tags/train/*) echo "reject train tag for test" >&2; exit 1 ;;
  esac
done
exit 0
`
	if err := os.WriteFile(hookPath, []byte(hookBody), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create release commit + tags locally.
	if err := os.WriteFile(filepath.Join(fx.Root, "modules/api/go.mod.note"), []byte("release marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runT(t, fx.Root, "git", "add", "-A")
	runT(t, fx.Root, "git", "commit", "-m", "release: train/test")

	tags := []string{
		"modules/storage/v0.9.0",
		"modules/api/v1.4.3",
		"train/2026-04-18-test",
	}
	for _, tg := range tags {
		runT(t, fx.Root, "git", "tag", tg, "HEAD")
	}

	err := AtomicPush(fx.Root, "origin", "main", tags)
	if err == nil {
		t.Fatal("AtomicPush succeeded but hook should have rejected")
	}

	remoteTags := listRemoteTags(t, fx.RemoteDir)
	for _, tg := range tags {
		if contains(remoteTags, tg) {
			t.Errorf("remote has tag %s after atomic rejection; should be absent", tg)
		}
	}

	remoteMain := remoteRefSHA(t, fx.RemoteDir, "refs/heads/main")
	// After the initial fixture commit, main on remote should be the fixture commit
	// (unchanged by this failed push).
	localFixtureCommit := runCapture(t, fx.Root, "git", "rev-parse", "HEAD~1")
	if strings.TrimSpace(remoteMain) != strings.TrimSpace(localFixtureCommit) {
		t.Errorf("remote main advanced despite atomic rejection: remote=%s local-pre-release=%s", remoteMain, localFixtureCommit)
	}
}

func listRemoteTags(t *testing.T, remoteDir string) []string {
	t.Helper()
	out := runCapture(t, remoteDir, "git", "tag")
	var tags []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			tags = append(tags, l)
		}
	}
	return tags
}

func remoteRefSHA(t *testing.T, remoteDir, ref string) string {
	t.Helper()
	return runCapture(t, remoteDir, "git", "rev-parse", ref)
}

func runCapture(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func runT(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./pocs/03-atomic-push/...
```
Expected: FAIL with `undefined: AtomicPush`.

- [ ] **Step 3: Implement `main.go`**

Create `/Users/matt/Developer/monoco/pocs/03-atomic-push/main.go`:
```go
// POC-3: prove that `git push --atomic origin main <tags...>` is truly
// all-or-nothing, including when a pre-receive hook rejects one ref.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// AtomicPush pushes the named branch and tags to remote as a single atomic unit.
// Returns an error if any ref is rejected; callers should treat that as
// "nothing landed" and rely on the remote being unchanged.
func AtomicPush(repoRoot, remote, branch string, tags []string) error {
	args := []string{"push", "--atomic", remote, branch}
	for _, t := range tags {
		args = append(args, "refs/tags/"+t)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %v: %w\nstderr: %s", args, err, stderr.String())
	}
	return nil
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: 03-atomic-push <repo-root> <remote> <branch> <tag>...")
		os.Exit(2)
	}
	if err := AtomicPush(os.Args[1], os.Args[2], os.Args[3], os.Args[4:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("OK")
}
```

- [ ] **Step 4: Run the tests**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./pocs/03-atomic-push/... -v
```
Expected: both subtests PASS.

**If `TestAtomicPush_rejectedByHook` shows partial tags landed**, `--atomic` is not behaving as advertised on this git version. Record the git version and failure mode in FINDINGS, and stop — this is a kill criterion.

- [ ] **Step 5: Commit**

```bash
cd /Users/matt/Developer/monoco && git add -A && git commit -m "feat(poc-3): atomic multi-tag push verified all-or-nothing"
```

---

## Task 5: Scale check for POC-1 (50-module fixture)

**Files:**
- Modify: `/Users/matt/Developer/monoco/pocs/01-revdep-graph/main_test.go` (add `TestAffectedSet_scale50`)

The simple-fixture performance assertion is <1s. Before concluding POC-1 is green, re-measure on a realistic monorepo size to ensure `go list -m -json all` fanout does not dominate at scale.

- [ ] **Step 1: Add a scale test**

Append to `/Users/matt/Developer/monoco/pocs/01-revdep-graph/main_test.go`:
```go
func TestAffectedSet_scale50(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test skipped under -short")
	}

	// Build a 50-module fixture: one leaf module "core", plus 49 modules
	// named "mod00".."mod48", each depending on the previous one (a chain).
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
```

Also add the import:
```go
import "fmt"
```

- [ ] **Step 2: Run the scale test**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./pocs/01-revdep-graph/... -v -run TestAffectedSet_scale50
```
Expected: PASS, with `BuildGraph` elapsed logged. Capture the number.

**Kill criterion:** if `BuildGraph` takes >10s, the v1 design must either parallelize the `go list` fanout or use an alternate data source (e.g., parsing go.mods directly and resolving only in-workspace edges, skipping `go list` entirely). Document the finding and stop.

- [ ] **Step 3: Commit**

```bash
cd /Users/matt/Developer/monoco && git add -A && git commit -m "test(poc-1): 50-module scale check"
```

---

## Task 6: Findings writeup

**Files:**
- Create: `/Users/matt/Developer/monoco/pocs/FINDINGS.md`

Synthesize results so the v1 implementation plan has ground truth.

- [ ] **Step 1: Write FINDINGS.md**

Create `/Users/matt/Developer/monoco/pocs/FINDINGS.md`:
```markdown
# monoco POC findings

_Date: <YYYY-MM-DD>_

## POC-1 — reverse-dep graph from `go list` + `go.work`

**Bet:** viable.

- Shape: parse `go.work` with `modfile.ParseWork`; for each `use` entry,
  read its `go.mod` for module path; run `go list -m -json all` with
  `GOWORK=off` to see that module's own dependency view; retain only edges
  whose target is also a workspace module.
- Simple fixture: `BuildGraph` ran in <measured-time>.
- Scale fixture (50 modules, worst-case chain): `BuildGraph` ran in <measured-time>.
- No Go-source parsing required.

**Recommendations for v1:**
- Parallelize `go list` fanout across modules from the start.
- Cache graph per-invocation (compute once per command run).

## POC-2 — go.mod verification without remote tags

**Bet:** <viable | viable-with-caveats | not-viable>.

- Primary approach (local tags + `GOPROXY=off GOFLAGS=-mod=mod`): <result>.
- Fallback approach (temporary replace directives): <result, if attempted>.
- Inconsistent rewrites are surfaced with error messages that mention the
  missing version.
- Working tree was/was-not unchanged after verify.

**Recommendations for v1:**
- Use <chosen approach> as the `verify` implementation.
- <any edge cases worth tracking>

## POC-3 — atomic multi-tag push

**Bet:** <viable | not-viable>.

- Happy path: all refs landed.
- Adversarial path (pre-receive hook rejects train tag): <none landed | some landed>.
- Git version tested: <output of `git --version`>.

**Recommendations for v1:**
- Use `git push --atomic` as the primary publish mechanism.
- <edge cases or follow-ups>

## Overall assessment

- Bets cleared: <list>
- Bets that need design revision: <list, if any>
- Ready to proceed to v1 implementation plan: <yes/no>
```

Fill in the `<placeholders>` from captured output of the test runs.

- [ ] **Step 2: Run all tests once more and capture output**

Run:
```bash
cd /Users/matt/Developer/monoco && go test ./... -v | tee pocs/_last-run.txt
```

Transfer numbers and results into `FINDINGS.md`. Delete `_last-run.txt` or add it to `.gitignore`.

- [ ] **Step 3: Commit**

```bash
cd /Users/matt/Developer/monoco && git add -A && git commit -m "docs(pocs): capture findings"
```

---

## Exit criteria

All POC tests pass. `FINDINGS.md` has concrete numbers and a yes/no on
each bet. The v1 implementation plan is written next, informed by
whatever POC-2 settled on (primary vs. fallback verification) and
POC-1's performance characteristics.

## Out of scope for this plan

- The monoco CLI itself (commands, flags, help). Comes in v1 plan.
- The `propagate` / `apply` / `plan` command implementations. Comes in v1 plan.
- The Conventional-Commits classifier. Comes in v1 plan.
- The task runner (`monoco test --since`). Comes in v1 plan.
- Manifest (`monoco.yaml`) schema and loader. Comes in v1 plan.
- CI integration (PR preview comments, GitHub Action). Follow-on.

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

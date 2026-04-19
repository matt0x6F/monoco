package propagate

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

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

func gitStatus(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	return string(out)
}

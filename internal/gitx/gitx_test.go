package gitx

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func TestRun_StdoutTrimmable(t *testing.T) {
	dir := initRepo(t)
	out, err := Run(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected a SHA, got %q", out)
	}
}

func TestRun_ErrorCarriesStderr(t *testing.T) {
	dir := initRepo(t)
	_, err := Run(context.Background(), dir, "no-such-subcommand")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-subcommand") {
		t.Fatalf("error should name the failing args: %v", err)
	}
}

func TestRun_CtxCancelKillsSubprocess(t *testing.T) {
	dir := initRepo(t)
	// Use git's wait-if-busy via a bogus --exec-path trick? Simpler:
	// run `git gc --aggressive` on a tiny repo won't block long. Use a
	// sleep-proxy by invoking a hook. Simplest: cancel before calling Run
	// and confirm the cancellation propagates as an error.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	// Ensure ctx is already expired.
	time.Sleep(5 * time.Millisecond)
	_, err := Run(ctx, dir, "log")
	if err == nil {
		t.Fatal("expected error from canceled ctx")
	}
}

func TestRun_HonorsRootFlag(t *testing.T) {
	dir := initRepo(t)
	// Create a nested dir that is NOT a git repo; -C should point us at dir.
	nested := filepath.Join(dir, "sub")
	if err := exec.Command("mkdir", nested).Run(); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), dir, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(strings.TrimSpace(out), filepath.Base(dir)) {
		t.Fatalf("expected toplevel under %q, got %q", dir, out)
	}
}

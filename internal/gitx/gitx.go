// Package gitx provides a single shared helper for invoking the git CLI.
// It exists so gitgraph, propagate, and release all produce identical
// error shapes and respect ctx cancellation uniformly.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Run executes `git -C root <args...>` and returns trimmed stdout.
// A nil ctx is treated as context.Background(). On non-zero exit the
// returned error carries the git args, the underlying err, and stderr.
func Run(ctx context.Context, root string, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	full := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, stderr.String())
	}
	return stdout.String(), nil
}

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
// If any ref is rejected, the remote is unchanged and an error is returned.
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

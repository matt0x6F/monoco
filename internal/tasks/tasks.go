// Package tasks runs a command in parallel across a set of workspace
// modules. Workspace mode is preserved so cross-module deps resolve to
// on-disk paths.
package tasks

import (
	"os"
	"os/exec"
	"sync"

	"github.com/matt0x6f/monoco/internal/workspace"
)

// Result captures one module's execution.
type Result struct {
	Module  string
	Command []string
	Dir     string
	Output  []byte // combined stdout+stderr
	Err     error  // non-nil if the command failed (non-zero exit)
}

// Run executes command in each named module's directory, in parallel.
// Modules not present in ws are skipped (with no entry in results).
// Preserves workspace mode (does NOT set GOWORK=off) so cross-module
// deps resolve to on-disk paths, which is what developers want for
// test/lint/build fanout.
func Run(ws *workspace.Workspace, modulePaths []string, command []string) []Result {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []Result
	)
	for _, mp := range modulePaths {
		mod, ok := ws.Modules[mp]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(mod workspace.Module) {
			defer wg.Done()
			cmd := exec.Command(command[0], command[1:]...)
			cmd.Dir = mod.Dir
			cmd.Env = os.Environ() // keep GOWORK on
			out, err := cmd.CombinedOutput()
			mu.Lock()
			results = append(results, Result{
				Module:  mod.Path,
				Command: command,
				Dir:     mod.Dir,
				Output:  out,
				Err:     err,
			})
			mu.Unlock()
		}(mod)
	}
	wg.Wait()
	return results
}

// AnyFailed reports whether any Result has Err != nil.
func AnyFailed(results []Result) bool {
	for _, r := range results {
		if r.Err != nil {
			return true
		}
	}
	return false
}

package propagate

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/matt0x6f/monoco/internal/workspace"
	"golang.org/x/mod/modfile"
)

// Verify runs a module-mode build of each named module with an alternate
// go.mod (-modfile) that adds replace directives redirecting every in-
// workspace require to its on-disk path. The real go.mod is not mutated;
// the alt modfile + sum are removed on return.
//
// Returns the first build failure encountered.
func Verify(ws *workspace.Workspace, modulePaths []string) error {
	for _, mp := range modulePaths {
		mod, ok := ws.Modules[mp]
		if !ok {
			return fmt.Errorf("module %q not found in workspace", mp)
		}
		if err := verifyOne(mod.Dir, ws); err != nil {
			return err
		}
	}
	return nil
}

func verifyOne(modDir string, ws *workspace.Workspace) error {
	goModPath := filepath.Join(modDir, "go.mod")
	orig, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", goModPath, err)
	}
	mf, err := modfile.Parse(goModPath, orig, nil)
	if err != nil {
		return fmt.Errorf("parse %s: %w", goModPath, err)
	}

	verify, err := modfile.Parse(goModPath, orig, nil)
	if err != nil {
		return err
	}
	for _, req := range mf.Require {
		dep, ok := ws.Modules[req.Mod.Path]
		if !ok {
			continue
		}
		rel, err := filepath.Rel(modDir, dep.Dir)
		if err != nil {
			return fmt.Errorf("relpath %s -> %s: %w", modDir, dep.Dir, err)
		}
		if err := verify.AddReplace(req.Mod.Path, "", rel, ""); err != nil {
			return fmt.Errorf("add replace for %s: %w", req.Mod.Path, err)
		}
	}
	verify.Cleanup()
	newBytes, err := verify.Format()
	if err != nil {
		return fmt.Errorf("format verify go.mod: %w", err)
	}

	altMod := filepath.Join(modDir, "go.verify.mod")
	altSum := filepath.Join(modDir, "go.verify.sum")
	if err := os.WriteFile(altMod, newBytes, 0o644); err != nil {
		return fmt.Errorf("write alt modfile: %w", err)
	}
	defer os.Remove(altMod)
	if err := os.WriteFile(altSum, nil, 0o644); err != nil {
		return fmt.Errorf("write alt sum: %w", err)
	}
	defer os.Remove(altSum)

	cmd := exec.Command("go", "build", "-modfile=go.verify.mod", "./...")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("verify build in %s: %w\nstderr: %s", modDir, err, stderr.String())
	}
	return nil
}

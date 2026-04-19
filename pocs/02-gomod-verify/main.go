// POC-2: verify rewritten go.mod files compile correctly BEFORE tags exist
// on any remote. Uses `go build -modfile=<alt>` with a generated alternate
// go.mod that preserves the rewritten require lines but redirects in-workspace
// deps to local paths via replace directives.
//
// Does not mutate the real go.mod. Exercises the rewritten require lines
// against local source; if the caller rewrote wrong (e.g., downstream module
// uses a symbol not present in the new upstream), the build fails loudly.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// Verify runs an alt-modfile build for each named workspace module.
// It does not mutate tracked files under root.
func Verify(root string, modules []string) error {
	dirs, err := workspaceModuleDirs(root)
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}
	for _, mod := range modules {
		dir, ok := dirs[mod]
		if !ok {
			return fmt.Errorf("module %q not found in go.work", mod)
		}
		if err := verifyOne(dir, dirs); err != nil {
			return err
		}
	}
	return nil
}

func verifyOne(modDir string, allDirs map[string]string) error {
	goModPath := filepath.Join(modDir, "go.mod")
	origBytes, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", goModPath, err)
	}
	mf, err := modfile.Parse(goModPath, origBytes, nil)
	if err != nil {
		return fmt.Errorf("parse %s: %w", goModPath, err)
	}

	// Clone the modfile, then add replace directives redirecting every
	// in-workspace require to its local dir.
	verify, err := modfile.Parse(goModPath, origBytes, nil)
	if err != nil {
		return err
	}
	for _, req := range mf.Require {
		depDir, isWorkspace := allDirs[req.Mod.Path]
		if !isWorkspace {
			continue
		}
		rel, err := filepath.Rel(modDir, depDir)
		if err != nil {
			return fmt.Errorf("relpath %s -> %s: %w", modDir, depDir, err)
		}
		// AddReplace(oldPath, oldVers, newPath, newVers).
		// Empty oldVers means "all versions of oldPath".
		if err := verify.AddReplace(req.Mod.Path, "", rel, ""); err != nil {
			return fmt.Errorf("add replace for %s: %w", req.Mod.Path, err)
		}
	}
	verify.Cleanup()
	newBytes, err := verify.Format()
	if err != nil {
		return fmt.Errorf("format verify go.mod: %w", err)
	}

	altPath := filepath.Join(modDir, "go.verify.mod")
	if err := os.WriteFile(altPath, newBytes, 0o644); err != nil {
		return fmt.Errorf("write alt modfile: %w", err)
	}
	defer os.Remove(altPath)
	altSum := filepath.Join(modDir, "go.verify.sum")
	if err := os.WriteFile(altSum, nil, 0o644); err != nil {
		return fmt.Errorf("write alt sum: %w", err)
	}
	defer os.Remove(altSum)

	cmd := exec.Command("go", "build", "-modfile=go.verify.mod", "./...")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("verify build in %s: %w\nstderr: %s", modDir, err, stderr.String())
	}
	return nil
}

// workspaceModuleDirs reads <root>/go.work and returns a map from module path
// (as declared in each module's go.mod) to its on-disk directory.
func workspaceModuleDirs(root string) (map[string]string, error) {
	workPath := filepath.Join(root, "go.work")
	workBytes, err := os.ReadFile(workPath)
	if err != nil {
		return nil, err
	}
	wf, err := modfile.ParseWork(workPath, workBytes, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, u := range wf.Use {
		modDir := filepath.Join(root, u.Path)
		b, err := os.ReadFile(filepath.Join(modDir, "go.mod"))
		if err != nil {
			return nil, err
		}
		mf, err := modfile.Parse(filepath.Join(modDir, "go.mod"), b, nil)
		if err != nil {
			return nil, err
		}
		out[mf.Module.Mod.Path] = modDir
	}
	return out, nil
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: 02-gomod-verify <repo-root> <module-path>...")
		os.Exit(2)
	}
	if err := Verify(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("OK")
}

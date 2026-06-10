package propagate

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

// ModuleHashes is the pair of h1: lines that go in a downstream's go.sum.
type ModuleHashes struct {
	H1    string // hash of the module zip (content hash)
	H1Mod string // hash of go.mod
}

// ComputeModuleHashes produces the canonical h1: hashes for a module
// rooted at modDir, as if it had been tagged as modPath@version.
// Validated by POC-4; see docs/poc-findings.md.
func ComputeModuleHashes(modDir, modPath, version string) (ModuleHashes, error) {
	tmp, err := os.CreateTemp("", "monoco-modzip-*.zip")
	if err != nil {
		return ModuleHashes{}, fmt.Errorf("create tmp zip: %w", err)
	}
	defer os.Remove(tmp.Name())

	if err := modzip.CreateFromDir(tmp, module.Version{Path: modPath, Version: version}, modDir); err != nil {
		tmp.Close()
		return ModuleHashes{}, fmt.Errorf("create module zip for %s@%s: %w", modPath, version, err)
	}
	if err := tmp.Close(); err != nil {
		return ModuleHashes{}, err
	}

	h1, err := dirhash.HashZip(tmp.Name(), dirhash.Hash1)
	if err != nil {
		return ModuleHashes{}, fmt.Errorf("hash zip: %w", err)
	}
	goModPath := filepath.Join(modDir, "go.mod")
	h1mod, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return os.Open(goModPath)
	})
	if err != nil {
		return ModuleHashes{}, fmt.Errorf("hash go.mod: %w", err)
	}
	return ModuleHashes{H1: h1, H1Mod: h1mod}, nil
}

// hashModuleWithOverlay computes the canonical h1: hashes for the module
// rooted at modDir as it will exist at the release commit: overlay maps
// module-relative file paths (slash-separated) to their post-rewrite
// contents. The module is staged into a temp dir so the working tree is
// never touched — dry-run and apply share this path.
func hashModuleWithOverlay(modDir, modPath, version string, overlay map[string][]byte) (ModuleHashes, error) {
	if len(overlay) == 0 {
		return ComputeModuleHashes(modDir, modPath, version)
	}
	tmp, err := os.MkdirTemp("", "monoco-modstage-*")
	if err != nil {
		return ModuleHashes{}, fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := stageDir(modDir, tmp); err != nil {
		return ModuleHashes{}, fmt.Errorf("stage %s: %w", modDir, err)
	}
	for rel, content := range overlay {
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return ModuleHashes{}, err
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			return ModuleHashes{}, err
		}
	}
	return ComputeModuleHashes(tmp, modPath, version)
}

// stageDir copies the regular files under src into dst, preserving
// relative layout. Non-regular files (symlinks, devices) are skipped —
// modzip.CreateFromDir omits them from module zips anyway, so the staged
// copy hashes identically. VCS metadata dirs are skipped for the same
// reason.
func stageDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			switch d.Name() {
			case ".git", ".hg", ".svn", ".bzr":
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

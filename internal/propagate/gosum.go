package propagate

import (
	"fmt"
	"io"
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
// Validated in pocs/04-release-gosum.
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

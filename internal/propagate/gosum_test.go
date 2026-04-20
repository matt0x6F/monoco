package propagate

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

// TestComputeModuleHashes_AgreesWithGoModDownload proves the table-stakes
// property for drop-in compatibility with any compliant Go module proxy
// (proxy.golang.org, Athens, JFrog, Nexus, GitLab, ...): the h1: lines
// monoco precomputes into downstream go.sum files are byte-for-byte what
// the go toolchain writes when it fetches the same module zip through a
// proxy.
//
// Approach: materialise a filesystem GOPROXY serving the target module,
// point a fresh consumer module at it, and run `go mod download`. The
// go.sum Go writes IS the canonical answer. We assert equality with our
// ComputeModuleHashes output.
//
// No network: the proxy is a local directory, GOSUMDB is off. Works in
// any environment with `go` on PATH.
func TestComputeModuleHashes_AgreesWithGoModDownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file:// GOPROXY URL construction is fiddly on Windows; skip")
	}

	const (
		targetPath = "example.test/target"
		version    = "v1.0.0"
	)

	// 1. Build the target module on disk.
	targetDir := t.TempDir()
	writeGosumTestFile(t, filepath.Join(targetDir, "go.mod"),
		"module "+targetPath+"\n\ngo 1.22\n")
	writeGosumTestFile(t, filepath.Join(targetDir, "lib.go"),
		"package target\n\n// Ping is a stand-in exported symbol.\nfunc Ping() string { return \"pong\" }\n")

	// 2. Ours.
	ours, err := ComputeModuleHashes(targetDir, targetPath, version)
	if err != nil {
		t.Fatalf("ComputeModuleHashes: %v", err)
	}
	if !strings.HasPrefix(ours.H1, "h1:") || !strings.HasPrefix(ours.H1Mod, "h1:") {
		t.Fatalf("expected h1: prefix, got zip=%q mod=%q", ours.H1, ours.H1Mod)
	}

	// 3. Materialise a filesystem GOPROXY layout:
	//    $proxy/<module path>/@v/{list, v1.0.0.info, v1.0.0.mod, v1.0.0.zip}
	proxyDir := t.TempDir()
	modAtV := filepath.Join(proxyDir, filepath.FromSlash(targetPath), "@v")
	if err := os.MkdirAll(modAtV, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGosumTestFile(t, filepath.Join(modAtV, "list"), version+"\n")
	writeGosumTestFile(t, filepath.Join(modAtV, version+".info"),
		`{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`+"\n")
	copyFile(t, filepath.Join(targetDir, "go.mod"), filepath.Join(modAtV, version+".mod"))

	zipPath := filepath.Join(modAtV, version+".zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := modzip.CreateFromDir(zf, module.Version{Path: targetPath, Version: version}, targetDir); err != nil {
		zf.Close()
		t.Fatalf("create proxy zip: %v", err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	// 4. Consumer module that requires target @ version.
	consumerDir := t.TempDir()
	writeGosumTestFile(t, filepath.Join(consumerDir, "go.mod"),
		"module example.test/consumer\n\ngo 1.22\n\nrequire "+targetPath+" "+version+"\n")

	// 5. `go mod download` through the filesystem proxy. Go writes its
	// own go.sum based on what it actually downloaded and hashed.
	proxyURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(proxyDir)}).String()
	gopath := t.TempDir()

	// Module cache files are written read-only; t.TempDir cleanup fails
	// on them. Purge the cache via `go clean -modcache` before Go's
	// cleanup tries to unlink.
	t.Cleanup(func() {
		clean := exec.Command("go", "clean", "-modcache")
		clean.Env = append(os.Environ(), "GOPATH="+gopath)
		_ = clean.Run()
	})

	cmd := exec.Command("go", "mod", "download", targetPath+"@"+version)
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(),
		"GOPROXY="+proxyURL,
		"GOSUMDB=off",
		"GOFLAGS=-mod=mod",
		"GOWORK=off",
		"GOPATH="+gopath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod download: %v\n%s", err, out)
	}

	// 6. Parse the consumer's go.sum and compare to ours.
	goH1, goH1Mod := readGoSum(t, filepath.Join(consumerDir, "go.sum"), targetPath, version)
	if goH1 == "" || goH1Mod == "" {
		t.Fatalf("go.sum missing entries for %s@%s; got zip=%q mod=%q", targetPath, version, goH1, goH1Mod)
	}

	if ours.H1 != goH1 {
		t.Errorf("h1 zip disagreement:\n  ours: %s\n  go:   %s", ours.H1, goH1)
	}
	if ours.H1Mod != goH1Mod {
		t.Errorf("h1 go.mod disagreement:\n  ours: %s\n  go:   %s", ours.H1Mod, goH1Mod)
	}
}

// readGoSum returns the zip and go.mod h1: values for modPath@version
// from a go.sum file. Empty strings if the line is missing.
func readGoSum(t *testing.T, path, modPath, version string) (h1Zip, h1Mod string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	zipPrefix := fmt.Sprintf("%s %s ", modPath, version)
	modPrefix := fmt.Sprintf("%s %s/go.mod ", modPath, version)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, zipPrefix):
			h1Zip = strings.TrimPrefix(line, zipPrefix)
		case strings.HasPrefix(line, modPrefix):
			h1Mod = strings.TrimPrefix(line, modPrefix)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return
}

func writeGosumTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

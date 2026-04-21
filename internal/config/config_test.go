package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoad_absent(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if c == nil {
		t.Fatal("Load returned nil config on absent manifest")
	}
	if len(c.Exclude) != 0 || len(c.Tasks) != 0 {
		t.Fatalf("absent manifest should yield zero-value config, got %+v", c)
	}
	if cmd := c.TaskCommand("test"); cmd != nil {
		t.Fatalf("TaskCommand on zero config: want nil, got %v", cmd)
	}
}

func TestLoad_parsesExcludesAndTasks(t *testing.T) {
	dir := writeManifest(t, `
version: 1
exclude:
  - modules/internal-experimental
  - ./modules/private-sdk
tasks:
  lint:
    command: ["golangci-lint", "run", "--timeout=5m"]
  generate:
    command: ["go", "generate", "./..."]
`)
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantExclude := []string{"modules/internal-experimental", "modules/private-sdk"}
	if !reflect.DeepEqual(c.Exclude, wantExclude) {
		t.Errorf("Exclude: got %v want %v", c.Exclude, wantExclude)
	}
	if got := c.TaskCommand("lint"); !reflect.DeepEqual(got, []string{"golangci-lint", "run", "--timeout=5m"}) {
		t.Errorf("lint command: got %v", got)
	}
	if got := c.TaskCommand("test"); got != nil {
		t.Errorf("unset task should return nil, got %v", got)
	}
	set := c.ExcludedSet()
	if _, ok := set["modules/internal-experimental"]; !ok {
		t.Errorf("ExcludedSet missing entry: %v", set)
	}
}

func TestLoad_rejectsUnknownTask(t *testing.T) {
	dir := writeManifest(t, `
version: 1
tasks:
  publish:
    command: ["echo", "hi"]
`)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("want unknown-task error, got %v", err)
	}
}

func TestLoad_rejectsEmptyTaskCommand(t *testing.T) {
	dir := writeManifest(t, `
version: 1
tasks:
  lint:
    command: []
`)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "at least one argv") {
		t.Fatalf("want empty-command error, got %v", err)
	}
}

func TestLoad_rejectsAbsoluteExclude(t *testing.T) {
	dir := writeManifest(t, `
version: 1
exclude:
  - /etc/passwd
`)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "repo-relative") {
		t.Fatalf("want abs-path error, got %v", err)
	}
}

func TestLoad_rejectsParentExclude(t *testing.T) {
	dir := writeManifest(t, `
version: 1
exclude:
  - ../outside
`)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "..") {
		t.Fatalf("want parent-ref error, got %v", err)
	}
}

func TestLoad_rejectsUnknownFields(t *testing.T) {
	dir := writeManifest(t, `
version: 1
bogus: true
`)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want unknown-field error, got %v", err)
	}
}

func TestLoad_parsesAllowMajor(t *testing.T) {
	dir := writeManifest(t, `
version: 1
allow_major:
  - example.com/acme/foo
  - example.com/acme/bar
`)
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	set := c.AllowMajorSet()
	if _, ok := set["example.com/acme/foo"]; !ok {
		t.Errorf("AllowMajorSet missing foo: %v", set)
	}
	if _, ok := set["example.com/acme/bar"]; !ok {
		t.Errorf("AllowMajorSet missing bar: %v", set)
	}
}

func TestLoad_rejectsEmptyAllowMajorEntry(t *testing.T) {
	dir := writeManifest(t, `
version: 1
allow_major:
  - ""
`)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "allow_major") {
		t.Fatalf("want allow_major error, got %v", err)
	}
}

func TestLoad_rejectsBadVersion(t *testing.T) {
	dir := writeManifest(t, `version: 2`)
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("want version error, got %v", err)
	}
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

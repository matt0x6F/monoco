package importrewrite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteModuleDirective_setsVN(t *testing.T) {
	in := []byte("module example.com/acme/foo\n\ngo 1.22\n")
	out, err := RewriteModuleDirective(in, "example.com/acme/foo/v2")
	if err != nil {
		t.Fatalf("RewriteModuleDirective: %v", err)
	}
	if !strings.Contains(string(out), "module example.com/acme/foo/v2") {
		t.Fatalf("expected rewritten module line, got:\n%s", out)
	}
}

func TestRewriteModuleDirective_idempotent(t *testing.T) {
	in := []byte("module example.com/acme/foo/v2\n\ngo 1.22\n")
	out, err := RewriteModuleDirective(in, "example.com/acme/foo/v2")
	if err != nil {
		t.Fatalf("RewriteModuleDirective: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("expected byte-identical output on idempotent call\ngot:  %q\nwant: %q", out, in)
	}
}

func TestRewriteConsumer_bareImport(t *testing.T) {
	dir := t.TempDir()
	src := `package foo

import "example.com/acme/bar"

var _ = bar.Hello
`
	writeGo(t, dir, "foo.go", src)

	rep, err := RewriteConsumer(dir, []Rewrite{{OldPath: "example.com/acme/bar", NewPath: "example.com/acme/bar/v2"}})
	if err != nil {
		t.Fatalf("RewriteConsumer: %v", err)
	}
	if len(rep.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d (%v)", len(rep.Changes), rep.Changes)
	}
	got := string(rep.Changes[0].New)
	if !strings.Contains(got, `"example.com/acme/bar/v2"`) {
		t.Fatalf("expected rewritten import, got:\n%s", got)
	}
}

func TestRewriteConsumer_aliasedImportPreserved(t *testing.T) {
	dir := t.TempDir()
	src := `package foo

import bar "example.com/acme/bar"

var _ = bar.Hello
`
	writeGo(t, dir, "foo.go", src)

	rep, err := RewriteConsumer(dir, []Rewrite{{OldPath: "example.com/acme/bar", NewPath: "example.com/acme/bar/v2"}})
	if err != nil {
		t.Fatalf("RewriteConsumer: %v", err)
	}
	if len(rep.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(rep.Changes))
	}
	got := string(rep.Changes[0].New)
	if !strings.Contains(got, `bar "example.com/acme/bar/v2"`) {
		t.Fatalf("expected alias preserved, got:\n%s", got)
	}
}

func TestRewriteConsumer_dotAndBlankImports(t *testing.T) {
	dir := t.TempDir()
	src := `package foo

import (
	. "example.com/acme/bar"
	_ "example.com/acme/bar/side"
)
`
	writeGo(t, dir, "foo.go", src)

	rep, err := RewriteConsumer(dir, []Rewrite{
		{OldPath: "example.com/acme/bar", NewPath: "example.com/acme/bar/v2"},
		{OldPath: "example.com/acme/bar/side", NewPath: "example.com/acme/bar/v2/side"},
	})
	if err != nil {
		t.Fatalf("RewriteConsumer: %v", err)
	}
	if len(rep.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(rep.Changes))
	}
	got := string(rep.Changes[0].New)
	if !strings.Contains(got, `. "example.com/acme/bar/v2"`) {
		t.Fatalf("expected dot import rewritten, got:\n%s", got)
	}
	if !strings.Contains(got, `_ "example.com/acme/bar/v2/side"`) {
		t.Fatalf("expected blank import rewritten, got:\n%s", got)
	}
}

func TestRewriteConsumer_skipsNonMatchingImports(t *testing.T) {
	dir := t.TempDir()
	src := `package foo

import (
	"example.com/acme/other"
)

var _ = other.X
`
	writeGo(t, dir, "foo.go", src)

	rep, err := RewriteConsumer(dir, []Rewrite{{OldPath: "example.com/acme/bar", NewPath: "example.com/acme/bar/v2"}})
	if err != nil {
		t.Fatalf("RewriteConsumer: %v", err)
	}
	if len(rep.Changes) != 0 {
		t.Fatalf("expected no changes, got %d", len(rep.Changes))
	}
}

func TestRewriteConsumer_skipsBuildTagMismatch(t *testing.T) {
	dir := t.TempDir()
	src := `//go:build never_satisfied_tag_xyz

package foo

import "example.com/acme/bar"

var _ = bar.Hello
`
	writeGo(t, dir, "foo_tagged.go", src)

	rep, err := RewriteConsumer(dir, []Rewrite{{OldPath: "example.com/acme/bar", NewPath: "example.com/acme/bar/v2"}})
	if err != nil {
		t.Fatalf("RewriteConsumer: %v", err)
	}
	if len(rep.Changes) != 0 {
		t.Fatalf("expected file to be skipped, got %d changes", len(rep.Changes))
	}
	if len(rep.SkippedFiles) != 1 {
		t.Fatalf("expected 1 skipped file, got %d", len(rep.SkippedFiles))
	}
}

func TestRewriteConsumer_skipsNestedModule(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "top.go", "package foo\n\nimport \"example.com/acme/bar\"\n\nvar _ = bar.Hello\n")

	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(nested, "go.mod"), "module example.com/other\n\ngo 1.22\n")
	writeGo(t, nested, "other.go", "package other\n\nimport \"example.com/acme/bar\"\n\nvar _ = bar.Hello\n")

	rep, err := RewriteConsumer(dir, []Rewrite{{OldPath: "example.com/acme/bar", NewPath: "example.com/acme/bar/v2"}})
	if err != nil {
		t.Fatalf("RewriteConsumer: %v", err)
	}
	if len(rep.Changes) != 1 {
		t.Fatalf("expected 1 change (only top.go), got %d", len(rep.Changes))
	}
	if !strings.HasSuffix(rep.Changes[0].Path, "top.go") {
		t.Fatalf("wrong file rewritten: %s", rep.Changes[0].Path)
	}
}

func writeGo(t *testing.T, dir, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, name), content)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

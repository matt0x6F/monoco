package main

import (
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func newWS() *workspace.Workspace {
	w := &workspace.Workspace{Modules: map[string]workspace.Module{}}
	w.Modules["example.com/foo"] = workspace.Module{Path: "example.com/foo", Dir: "/tmp/foo", RelDir: "foo"}
	w.Modules["example.com/bar"] = workspace.Module{Path: "example.com/bar", Dir: "/tmp/bar", RelDir: "bar"}
	return w
}

func TestParseBumpFlags(t *testing.T) {
	ws := newWS()

	cases := []struct {
		name    string
		in      []string
		want    map[string]bump.Kind
		wantErr string
	}{
		{"by module path", []string{"example.com/foo=minor"},
			map[string]bump.Kind{"example.com/foo": bump.Minor}, ""},
		{"by reldir", []string{"bar=patch"},
			map[string]bump.Kind{"example.com/bar": bump.Patch}, ""},
		{"skip", []string{"example.com/foo=skip"},
			map[string]bump.Kind{"example.com/foo": bump.Skip}, ""},
		{"multiple", []string{"example.com/foo=major", "bar=patch"},
			map[string]bump.Kind{"example.com/foo": bump.Major, "example.com/bar": bump.Patch}, ""},
		{"missing kind", []string{"example.com/foo="}, nil, "want <module>=<kind>"},
		{"missing module", []string{"=minor"}, nil, "want <module>=<kind>"},
		{"unknown module", []string{"example.com/baz=minor"}, nil, "not found"},
		{"unknown kind", []string{"example.com/foo=feat"}, nil, "invalid bump kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBumpFlags(ws, tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("got[%s] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

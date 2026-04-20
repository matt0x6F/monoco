package main

import (
	"strings"
	"testing"

	"github.com/matt0x6f/monoco/internal/propagate"
	"github.com/matt0x6f/monoco/internal/workspace"
)

func TestSplitModulesList(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"a", []string{"a"}, false},
		{"a,b,c", []string{"a", "b", "c"}, false},
		{" a , b ", []string{"a", "b"}, false},
		{"a,,b", nil, true},
		{",a", nil, true},
		{"", nil, true},
	}
	for _, tc := range cases {
		got, err := splitModulesList(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("splitModulesList(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("splitModulesList(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitModulesList(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestBuildPropagatePlan_mutuallyExclusiveFlags(t *testing.T) {
	// Workspace isn't touched on the rejection path; a zero workspace is fine.
	ws := &workspace.Workspace{Modules: map[string]workspace.Module{}}

	_, err := buildPropagatePlan(ws, "HEAD~1", "modules/api", propagate.Options{})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error, got: %v", err)
	}

	_, err = buildPropagatePlan(ws, "", "", propagate.Options{})
	if err == nil || !strings.Contains(err.Error(), "must specify") {
		t.Errorf("expected 'must specify' error, got: %v", err)
	}
}

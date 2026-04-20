package bump

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
		err  bool
	}{
		{"major", Major, false},
		{"Minor", Minor, false},
		{"PATCH", Patch, false},
		{" skip ", Skip, false},
		{"", None, true},
		{"feat", None, true},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if (err != nil) != c.err {
			t.Errorf("Parse(%q) err=%v, wantErr=%v", c.in, err, c.err)
		}
		if !c.err && got != c.want {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNextVersion_appliesBumpsCorrectly(t *testing.T) {
	cases := []struct {
		cur  string
		kind Kind
		want string
	}{
		// v0: major coerced to minor.
		{"v0.1.3", Patch, "v0.1.4"},
		{"v0.1.3", Minor, "v0.2.0"},
		{"v0.1.3", Major, "v0.2.0"},
		// v1+ normal semver.
		{"v1.2.3", Patch, "v1.2.4"},
		{"v1.2.3", Minor, "v1.3.0"},
		{"v1.2.3", Major, "v2.0.0"},
		// First release.
		{"", Patch, "v0.1.0"},
		{"", Minor, "v0.1.0"},
		{"", Major, "v0.1.0"},
		// Skip and None leave cur alone.
		{"v1.2.3", Skip, "v1.2.3"},
		{"v1.2.3", None, "v1.2.3"},
		{"", Skip, ""},
	}
	for _, c := range cases {
		got, err := NextVersion(c.cur, c.kind)
		if err != nil {
			t.Errorf("NextVersion(%q, %v) err: %v", c.cur, c.kind, err)
			continue
		}
		if got != c.want {
			t.Errorf("NextVersion(%q, %v) = %q; want %q", c.cur, c.kind, got, c.want)
		}
	}
}

func TestNextVersion_rejectsInvalid(t *testing.T) {
	if _, err := NextVersion("not-a-version", Patch); err == nil {
		t.Error("expected error for invalid current version")
	}
}

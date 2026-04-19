package convco

import "testing"

func TestClassify_breakingChange(t *testing.T) {
	// Bang in type marker.
	if got := Classify("feat(storage)!: drop legacy API", ""); got != Major {
		t.Errorf("bang marker: got %v, want Major", got)
	}
	// BREAKING CHANGE footer.
	body := "Some body.\n\nBREAKING CHANGE: removed Foo\n"
	if got := Classify("feat(api): rework", body); got != Major {
		t.Errorf("BREAKING CHANGE footer: got %v, want Major", got)
	}
}

func TestClassify_featIsMinor(t *testing.T) {
	if got := Classify("feat(api): add Batch method", ""); got != Minor {
		t.Errorf("feat: got %v, want Minor", got)
	}
}

func TestClassify_fixAndPerfArePatch(t *testing.T) {
	if got := Classify("fix(api): handle nil", ""); got != Patch {
		t.Errorf("fix: got %v, want Patch", got)
	}
	if got := Classify("perf(storage): avoid alloc", ""); got != Patch {
		t.Errorf("perf: got %v, want Patch", got)
	}
}

func TestClassify_choreIsNone(t *testing.T) {
	if got := Classify("chore: bump deps", ""); got != None {
		t.Errorf("chore: got %v, want None", got)
	}
	if got := Classify("docs: readme typo", ""); got != None {
		t.Errorf("docs: got %v, want None", got)
	}
}

func TestAggregate_takesHighest(t *testing.T) {
	kinds := []Kind{Patch, Minor, None}
	if got := Aggregate(kinds); got != Minor {
		t.Errorf("Aggregate = %v; want Minor", got)
	}
	kinds = []Kind{Patch, Major, Minor}
	if got := Aggregate(kinds); got != Major {
		t.Errorf("Aggregate = %v; want Major", got)
	}
	if got := Aggregate(nil); got != None {
		t.Errorf("Aggregate(nil) = %v; want None", got)
	}
}

func TestNextVersion_appliesBumpsCorrectly(t *testing.T) {
	cases := []struct {
		cur  string
		kind Kind
		want string
	}{
		// v0 behavior: major-tagged-as-minor, minor-and-patch as stated.
		{"v0.1.3", Patch, "v0.1.4"},
		{"v0.1.3", Minor, "v0.2.0"},
		{"v0.1.3", Major, "v0.2.0"}, // v0 treats "major" as minor
		// v1+ normal semver.
		{"v1.2.3", Patch, "v1.2.4"},
		{"v1.2.3", Minor, "v1.3.0"},
		{"v1.2.3", Major, "v2.0.0"},
		// First release when current is empty.
		{"", Patch, "v0.1.0"},
		{"", Minor, "v0.1.0"},
		{"", Major, "v0.1.0"},
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

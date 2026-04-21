package propagate

import "testing"

func TestValidateModuleTag(t *testing.T) {
	cases := []struct {
		in    string
		valid bool
	}{
		{"modules/storage/v0.1.0", true},
		{"modules/api/v1.2.3-rc.1", true},
		{"v2/modules/api/v2.0.0", true},
		{"", false},
		{"../evil/v0.1.0", false},
		{"modules/..//v0.1.0", false},
		{"modules/storage/v0.1", false},
		{"modules/storage/0.1.0", false},
		{"modules/storage/v0.1.0+build", false},
		{"modules/store age/v0.1.0", false},
		{"modules/store\tage/v0.1.0", false},
		{"modules/store\x00/v0.1.0", false},
		{"/modules/storage/v0.1.0", false},
		{"-modules/storage/v0.1.0", false},
	}
	for _, tc := range cases {
		err := validateModuleTag(tc.in)
		if (err == nil) != tc.valid {
			t.Errorf("validateModuleTag(%q) err=%v, want valid=%v", tc.in, err, tc.valid)
		}
	}
}

func TestValidateTrainTag(t *testing.T) {
	cases := []struct {
		in    string
		valid bool
	}{
		{"train/2026-04-21-release", true},
		{"train/2026-04-21-feat-foo.bar_baz", true},
		{"release/2026-04-21-feat", false},
		{"train/20260421-release", false},
		{"train/2026-04-21-", false},
		{"train/2026-04-21-HASUPPER", false},
		{"train/2026-04-21-with space", false},
	}
	for _, tc := range cases {
		err := validateTrainTag(tc.in)
		if (err == nil) != tc.valid {
			t.Errorf("validateTrainTag(%q) err=%v, want valid=%v", tc.in, err, tc.valid)
		}
	}
}

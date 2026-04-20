// Package bump owns semver bump kinds and the rules for applying them.
// Bump intent is declared at release time — no commit-message inference.
package bump

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

// Kind is a semver bump kind.
type Kind int

const (
	None Kind = iota
	Patch
	Minor
	Major
	Skip // user-chosen: do not release this module
)

func (k Kind) String() string {
	switch k {
	case None:
		return "none"
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	case Skip:
		return "skip"
	}
	return "unknown"
}

// Parse accepts "major"/"minor"/"patch"/"skip" (case-insensitive) and returns
// the matching Kind. Unknown strings return None and an error.
func Parse(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "major":
		return Major, nil
	case "minor":
		return Minor, nil
	case "patch":
		return Patch, nil
	case "skip":
		return Skip, nil
	}
	return None, fmt.Errorf("invalid bump kind %q (want major/minor/patch/skip)", s)
}

// NextVersion applies kind to cur and returns the new version. Pre-1.0
// versions (v0.x.y) treat Major as Minor — v0 is explicitly unstable; use
// v1+ for semver-major semantics. Empty cur ("never tagged") returns v0.1.0
// for any non-None, non-Skip kind.
func NextVersion(cur string, kind Kind) (string, error) {
	if kind == None || kind == Skip {
		return cur, nil
	}
	if cur == "" {
		return "v0.1.0", nil
	}
	if !semver.IsValid(cur) {
		return "", fmt.Errorf("invalid current version %q", cur)
	}
	major, minor, patch, err := parseSemver(cur)
	if err != nil {
		return "", err
	}
	if major == 0 {
		switch kind {
		case Major, Minor:
			minor++
			patch = 0
		case Patch:
			patch++
		}
	} else {
		switch kind {
		case Major:
			major++
			minor = 0
			patch = 0
		case Minor:
			minor++
			patch = 0
		case Patch:
			patch++
		}
	}
	return fmt.Sprintf("v%d.%d.%d", major, minor, patch), nil
}

func parseSemver(v string) (int, int, int, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("not a MAJOR.MINOR.PATCH string: %q", v)
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, err
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, err
	}
	patchStr := parts[2]
	for i, c := range patchStr {
		if c == '-' || c == '+' {
			patchStr = patchStr[:i]
			break
		}
	}
	pat, err := strconv.Atoi(patchStr)
	if err != nil {
		return 0, 0, 0, err
	}
	return maj, min, pat, nil
}

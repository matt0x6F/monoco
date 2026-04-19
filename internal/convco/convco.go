// Package convco parses Conventional Commits messages and classifies
// them into semver bump kinds.
package convco

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
	}
	return "unknown"
}

// Classify returns the bump kind implied by one commit message.
// Rules (from the design spec):
//   - "!" after type OR "BREAKING CHANGE" in body    → Major
//   - type "feat"                                    → Minor
//   - type "fix", "perf", "refactor"                 → Patch
//   - anything else                                  → None
func Classify(subject, body string) Kind {
	head := subject
	if i := strings.IndexByte(subject, ':'); i >= 0 {
		head = subject[:i]
	}
	if strings.Contains(head, "!") {
		return Major
	}
	if strings.Contains(body, "BREAKING CHANGE") {
		return Major
	}
	// Extract the type token (everything before ( or :).
	typ := head
	if i := strings.IndexByte(head, '('); i >= 0 {
		typ = head[:i]
	}
	typ = strings.TrimSpace(typ)
	switch typ {
	case "feat":
		return Minor
	case "fix", "perf", "refactor":
		return Patch
	}
	return None
}

// Aggregate returns the highest (most impactful) kind in the slice.
func Aggregate(kinds []Kind) Kind {
	top := None
	for _, k := range kinds {
		if k > top {
			top = k
		}
	}
	return top
}

// NextVersion applies the bump kind to cur and returns the new version.
// Pre-1.0 versions (v0.x.y) treat "major" as minor (v0 is explicitly
// unstable; use v1+ for semver-major meaning).
// An empty cur means "this module has never been tagged" → returns v0.1.0
// for any non-None kind.
func NextVersion(cur string, kind Kind) (string, error) {
	if kind == None {
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
		// v0: bump major → minor, minor → minor, patch → patch.
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
	// Patch may carry a pre-release/build metadata suffix; strip it.
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

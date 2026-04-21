// Package gitgraph provides read-only git operations used by monoco:
// commit-range queries, touched-file enumeration, and tag lookups.
package gitgraph

import (
	"context"
	"sort"
	"strings"

	"github.com/matt0x6f/monoco/internal/gitx"
	"golang.org/x/mod/semver"
)

// Commit is a minimal commit record.
type Commit struct {
	SHA     string
	Subject string
	Body    string
}

// TouchedFiles returns repo-relative paths of files changed between oldRef
// and newRef (both inclusive of HEAD side per git diff --name-only).
func TouchedFiles(root, oldRef, newRef string) ([]string, error) {
	out, err := gitx.Run(context.Background(), root, "diff", "--name-only", oldRef, newRef)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// CommitsInRange returns commits in (oldRef, newRef] (excluding oldRef,
// including newRef) that touched the given path (directory or file).
// The path is repo-relative.
func CommitsInRange(root, oldRef, newRef, path string) ([]Commit, error) {
	// Use printable multi-char separators: exec argv cannot contain NUL on darwin.
	const sep = "\x1f"    // ASCII unit separator
	const recSep = "\x1e" // ASCII record separator
	format := "%H" + sep + "%s" + sep + "%b" + recSep
	rangeSpec := oldRef + ".." + newRef
	args := []string{"log", "--format=" + format, rangeSpec}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := gitx.Run(context.Background(), root, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, recSep) {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		fields := strings.SplitN(rec, sep, 3)
		if len(fields) < 3 {
			continue
		}
		commits = append(commits, Commit{
			SHA:     fields[0],
			Subject: fields[1],
			Body:    fields[2],
		})
	}
	return commits, nil
}

// LatestTagForModule returns the highest-semver tag for a module whose
// tag-prefix is modulePrefix (e.g. "modules/storage"). Returns "" if none.
func LatestTagForModule(root, modulePrefix string) (string, error) {
	out, err := gitx.Run(context.Background(), root, "tag", "--list", modulePrefix+"/v*")
	if err != nil {
		return "", err
	}
	var versions []string
	prefix := modulePrefix + "/"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		v := strings.TrimPrefix(line, prefix)
		if !semver.IsValid(v) {
			continue
		}
		versions = append(versions, v)
	}
	if len(versions) == 0 {
		return "", nil
	}
	sort.Slice(versions, func(i, j int) bool {
		return semver.Compare(versions[i], versions[j]) < 0
	})
	return versions[len(versions)-1], nil
}

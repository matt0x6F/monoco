package release

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/matt0x6f/monoco/internal/bump"
	"github.com/matt0x6f/monoco/internal/gitgraph"
	"github.com/matt0x6f/monoco/internal/propagate"
	"github.com/matt0x6f/monoco/internal/workspace"
)

// TrailerKey is the commit-message trailer that declares a bump kind:
//
//	Monoco-Bump: <module>=<major|minor|patch|skip>
//
// It is the CI twin of the --bump flag. Flags die at the merge
// boundary — the developer who knows a PR deserves a minor isn't the
// one running `monoco release -y` on the push to main — so the
// declaration rides in the merged commit's message instead, and the
// next release picks it up. Same value syntax, same module-ref
// resolution, same validation as --bump.
const TrailerKey = "Monoco-Bump"

// TrailerDecl is one Monoco-Bump declaration found in a commit message.
type TrailerDecl struct {
	ModulePath string // canonical module path (resolved from the ref as written)
	Kind       bump.Kind
	SHA        string // commit that carried the declaration
}

// trailerLineRE matches a Monoco-Bump line anywhere in a commit
// message, key case-insensitive. Leading whitespace and a `* `/`- `
// bullet are tolerated so a declaration survives GitHub's squash-merge
// format, which folds branch commit subjects into a bulleted list.
var trailerLineRE = regexp.MustCompile(`(?i)^[ \t]*[*-]?[ \t]*` + TrailerKey + `:[ \t]*(\S+)[ \t]*$`)

// TrailerBumps scans commit messages between the latest reachable
// train tag and HEAD (all history when no train tag exists yet) for
// Monoco-Bump trailers. The returned map is the merged per-module
// intent; decls lists every declaration found (for display); since is
// the train tag that anchored the scan ("" if none). The window means
// each declaration is consumed by exactly one release: the train tag
// it ships under moves the anchor past it.
//
// When commits disagree about a module, the larger kind wins
// (skip < patch < minor < major) — a skip never suppresses another
// commit's explicit release request.
//
// A malformed value or unknown module is an error, not a no-op: a typo
// that silently under-releases is the one failure mode this channel
// must not have. Recovery: cut the release with --no-trailers plus
// explicit --bump flags; its train tag moves the window past the bad
// trailer.
func TrailerBumps(ws *workspace.Workspace) (merged map[string]bump.Kind, decls []TrailerDecl, since string, err error) {
	since, err = gitgraph.LatestTrainTag(ws.Root)
	if err != nil {
		return nil, nil, "", fmt.Errorf("find latest train tag: %w", err)
	}
	commits, err := gitgraph.CommitsSince(ws.Root, since)
	if err != nil {
		return nil, nil, "", fmt.Errorf("scan commits for %s trailers: %w", TrailerKey, err)
	}

	merged = map[string]bump.Kind{}
	for _, c := range commits {
		for _, line := range strings.Split(c.Subject+"\n"+c.Body, "\n") {
			m := trailerLineRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			val := m[1]
			eq := strings.IndexByte(val, '=')
			if eq < 1 || eq == len(val)-1 {
				return nil, nil, "", fmt.Errorf("commit %.7s: %s %q: want <module>=<major|minor|patch|skip>", c.SHA, TrailerKey, val)
			}
			ref, kindStr := val[:eq], val[eq+1:]
			mp, ok := propagate.ResolveModuleRef(ws, ref)
			if !ok {
				return nil, nil, "", fmt.Errorf("commit %.7s: %s %q: module %q not found in workspace (fix the trailer in a new commit, or release with --no-trailers and explicit --bump flags)", c.SHA, TrailerKey, val, ref)
			}
			k, perr := bump.Parse(kindStr)
			if perr != nil {
				return nil, nil, "", fmt.Errorf("commit %.7s: %s %q: %w", c.SHA, TrailerKey, val, perr)
			}
			decls = append(decls, TrailerDecl{ModulePath: mp, Kind: k, SHA: c.SHA})
			if cur, set := merged[mp]; !set || trailerRank(k) > trailerRank(cur) {
				merged[mp] = k
			}
		}
	}
	return merged, decls, since, nil
}

// trailerRank orders kinds for cross-commit conflict resolution. Skip
// is lowest: it holds only when no other commit asked to release.
func trailerRank(k bump.Kind) int {
	switch k {
	case bump.Skip:
		return 0
	case bump.Patch:
		return 1
	case bump.Minor:
		return 2
	case bump.Major:
		return 3
	}
	return -1
}

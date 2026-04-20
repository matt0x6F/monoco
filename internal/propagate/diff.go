package propagate

import (
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// UnifiedDiff renders a unified diff (3 lines of context) between oldBytes
// and newBytes. The displayPath is used in the `---` / `+++` headers; the
// "+++" side is suffixed with " (proposed)" so reviewers can tell at a glance
// that the right-hand side hasn't been written to disk yet.
//
// If the inputs are byte-identical, returns the empty string.
func UnifiedDiff(displayPath string, oldBytes, newBytes []byte) string {
	if string(oldBytes) == string(newBytes) {
		return ""
	}
	d := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(oldBytes)),
		B:        difflib.SplitLines(string(newBytes)),
		FromFile: displayPath,
		ToFile:   displayPath + " (proposed)",
		Context:  3,
	}
	out, err := difflib.GetUnifiedDiffString(d)
	if err != nil {
		// difflib's writer only fails if the underlying io.Writer fails;
		// strings.Builder never errors. Treat as no-diff rather than panic.
		return ""
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

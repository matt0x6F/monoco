package release

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/matt0x6f/monoco/internal/bump"
)

// StdioPrompter implements Prompter against an io.Reader (input) and
// io.Writer (output). Re-prompts on invalid input.
type StdioPrompter struct {
	In  *bufio.Reader
	Out io.Writer
}

// NewStdioPrompter wraps a raw reader.
func NewStdioPrompter(in io.Reader, out io.Writer) *StdioPrompter {
	return &StdioPrompter{In: bufio.NewReader(in), Out: out}
}

// Ask prompts once for a bump kind. Empty input defaults to Skip if
// cascade (direct=false), else re-prompts.
func (p *StdioPrompter) Ask(modPath, curVersion string, direct bool) (bump.Kind, error) {
	cv := curVersion
	if cv == "" {
		cv = "(none)"
	}
	tag := "direct"
	if !direct {
		tag = "cascade"
	}
	for {
		fmt.Fprintf(p.Out, "%s  (currently %s, %s) — bump? [major/minor/patch/skip]: ", modPath, cv, tag)
		line, err := p.In.ReadString('\n')
		if err != nil && line == "" {
			return bump.None, fmt.Errorf("read input: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Fprintln(p.Out, "  (please enter one of major/minor/patch/skip)")
			continue
		}
		k, perr := bump.Parse(line)
		if perr != nil {
			fmt.Fprintf(p.Out, "  invalid: %v\n", perr)
			continue
		}
		return k, nil
	}
}

// ConfirmProceed reads a y/N from r, returning true iff input starts with
// 'y' or 'Y'. Used after printing the plan.
func ConfirmProceed(in *bufio.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "Proceed? [y/N]: ")
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	line = strings.TrimSpace(line)
	return len(line) > 0 && (line[0] == 'y' || line[0] == 'Y'), nil
}

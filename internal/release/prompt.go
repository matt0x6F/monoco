package release

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// ConfirmProceed reads a y/N from in, returning true iff input starts
// with 'y' or 'Y'. Used after printing the plan, before applying.
// Bypass with the `-y` CLI flag.
func ConfirmProceed(in *bufio.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "Proceed? [y/N]: ")
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	line = strings.TrimSpace(line)
	return len(line) > 0 && (line[0] == 'y' || line[0] == 'Y'), nil
}

// Package config loads the optional monoco.yaml manifest at the repo root.
//
// The manifest is convention-over-configuration: in its absence, monoco
// uses its built-in defaults (every go.mod-bearing dir is a propagatable
// module; task commands are `go test ./...`, `golangci-lint run`, etc.).
// The manifest exists so teams with one-off exceptions (an unreleased
// internal module; a custom lint invocation) can deviate without giving
// up the defaults elsewhere.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Filename is the manifest filename looked up at the repo root.
const Filename = "monoco.yaml"

// Config is the parsed manifest. An absent manifest yields a zero-value
// Config — semantically identical to "no overrides."
type Config struct {
	// Version is the manifest schema version. Currently only 1 is valid.
	Version int `yaml:"version"`

	// Exclude is a list of repo-relative module directories (the same
	// form that appears in go.work's `use` entries, e.g. `modules/foo`)
	// that monoco will omit from propagation, affected-set computation,
	// and task fanout. Forward slashes; cleaned on load.
	Exclude []string `yaml:"exclude"`

	// Tasks overrides the command executed for a given task name.
	// Recognized names: test, lint, build, generate. Unknown names are
	// rejected. Any task name not present keeps its built-in default.
	Tasks map[string]Task `yaml:"tasks"`

	// AllowMajor lists module paths (as they appear in `go.mod`'s
	// `module` line, without any `/vN` suffix) that may cross a major
	// version boundary in a propagation. Absent entries are still
	// rejected with a clear error — this gate is intentionally
	// opt-in per module.
	AllowMajor []string `yaml:"allow_major"`
}

// Task is one task-command override.
type Task struct {
	// Command is the argv executed per module. Must be non-empty when a
	// Task entry is present.
	Command []string `yaml:"command"`
}

// knownTasks is the fixed set of task names the CLI dispatches.
// Keep in sync with cmd/monoco/main.go's switch.
var knownTasks = map[string]struct{}{
	"test":     {},
	"lint":     {},
	"build":    {},
	"generate": {},
}

// Load reads <root>/monoco.yaml. If the file does not exist, it returns
// a zero-value Config and nil error: absence is not an error.
// Parse or validation errors are returned as-is.
func Load(root string) (*Config, error) {
	path := filepath.Join(root, Filename)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", Filename, err)
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Filename, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", Filename, err)
	}
	c.normalize()
	return &c, nil
}

// ExcludedSet returns the exclude list as a set of cleaned, slash-form
// repo-relative directories. Convenient for O(1) lookup at load time.
func (c *Config) ExcludedSet() map[string]struct{} {
	out := make(map[string]struct{}, len(c.Exclude))
	for _, e := range c.Exclude {
		out[e] = struct{}{}
	}
	return out
}

// AllowMajorSet returns AllowMajor as a set for O(1) membership checks.
func (c *Config) AllowMajorSet() map[string]struct{} {
	if c == nil {
		return nil
	}
	out := make(map[string]struct{}, len(c.AllowMajor))
	for _, m := range c.AllowMajor {
		out[m] = struct{}{}
	}
	return out
}

// TaskCommand returns the configured command for task, or nil if the
// manifest does not override it. Callers should use their built-in
// default on nil.
func (c *Config) TaskCommand(task string) []string {
	if c == nil {
		return nil
	}
	t, ok := c.Tasks[task]
	if !ok {
		return nil
	}
	return t.Command
}

func (c *Config) validate() error {
	if c.Version != 0 && c.Version != 1 {
		return fmt.Errorf("version: unsupported value %d (only 1 is recognized)", c.Version)
	}
	for i, e := range c.Exclude {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("exclude[%d]: empty entry", i)
		}
		if filepath.IsAbs(e) {
			return fmt.Errorf("exclude[%d] %q: must be a repo-relative path, not absolute", i, e)
		}
		if strings.Contains(e, "..") {
			return fmt.Errorf("exclude[%d] %q: must not contain `..`", i, e)
		}
	}
	for i, m := range c.AllowMajor {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("allow_major[%d]: empty entry", i)
		}
	}
	for name, t := range c.Tasks {
		if _, ok := knownTasks[name]; !ok {
			return fmt.Errorf("tasks.%s: unknown task (want one of test/lint/build/generate)", name)
		}
		if len(t.Command) == 0 {
			return fmt.Errorf("tasks.%s.command: must have at least one argv element", name)
		}
		for j, a := range t.Command {
			if a == "" {
				return fmt.Errorf("tasks.%s.command[%d]: empty argv element", name, j)
			}
		}
	}
	return nil
}

func (c *Config) normalize() {
	for i, e := range c.Exclude {
		e = filepath.ToSlash(filepath.Clean(e))
		e = strings.TrimPrefix(e, "./")
		c.Exclude[i] = e
	}
}

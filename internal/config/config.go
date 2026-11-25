// Package config loads, resolves and saves the oym-ai-jail YAML configuration.
//
// The file lives at ~/.config/oym-ai-jail/config.yaml (override with
// OYM_AI_JAIL_CONFIG). Paths may use "~"; they are canonicalised (symlinks
// resolved) before they reach the Seatbelt profile, because Seatbelt only
// ever sees real paths.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvConfigPath overrides the config file location.
const EnvConfigPath = "OYM_AI_JAIL_CONFIG"

const (
	dirName  = "oym-ai-jail"
	fileName = "config.yaml"
)

// CustomAgent wraps an arbitrary CLI so it also runs inside the jail.
type CustomAgent struct {
	Name    string   `yaml:"name"`
	Command string   `yaml:"command,omitempty"` // defaults to Name
	Args    []string `yaml:"args,omitempty"`    // injected before user args
	RWPaths []string `yaml:"rw_paths,omitempty"`
}

// Config is the raw YAML model. Paths stay as the user wrote them ("~/...")
// so saving round-trips cleanly; Resolved() produces the canonical view the
// sandbox consumes.
type Config struct {
	Version             int             `yaml:"version"`
	Workspaces          []string        `yaml:"workspaces"`
	AllowWorkspaceRead  bool            `yaml:"allow_workspace_read"`
	AllowWorkspaceWrite bool            `yaml:"allow_workspace_write"`
	ReadPaths           []string        `yaml:"read_paths"`
	WritePaths          []string        `yaml:"write_paths"`
	DenyReadPaths       []string        `yaml:"deny_read_paths,omitempty"`
	Agents              map[string]bool `yaml:"agents"`
	CustomAgents        []CustomAgent   `yaml:"custom_agents"`
}

// Default returns a first-run configuration for home.
func Default() *Config {
	return &Config{
		Version:             1,
		AllowWorkspaceRead:  true,
		AllowWorkspaceWrite: false,
		ReadPaths:           []string{"~/ai-allowed"},
		WritePaths:          []string{},
		Agents:              map[string]bool{},
		CustomAgents:        []CustomAgent{},
	}
}

// Path returns the config file location.
func Path() (string, error) {
	if override := os.Getenv(EnvConfigPath); override != "" {
		return override, nil
	}

	home, err := os.UserHomeDir()

	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".config", dirName, fileName), nil
}

// PeekAgentNames best-effort extracts every configured agent name using a
// lenient (non-strict) decode. shell-init uses it when the real load fails:
// the fail-closed wrappers should also cover custom agents, whose names a
// strict-decode error might otherwise hide.
func PeekAgentNames() []string {
	path, err := Path()

	if err != nil {
		return nil
	}

	data, err := os.ReadFile(path)

	if err != nil {
		return nil
	}
	var cfg Config
	// Even a partial decode may already contain agent names.
	_ = yaml.Unmarshal(data, &cfg)
	var names []string

	for name, on := range cfg.Agents {
		if on {
			names = append(names, name)
		}
	}

	for _, c := range cfg.CustomAgents {
		names = append(names, c.Name)
	}

	return names
}

// Load reads the config file. It returns (nil, false, nil) when the file
// does not exist yet so callers can offer first-run setup.
func Load() (*Config, bool, error) {
	path, err := Path()

	if err != nil {
		return nil, false, err
	}

	return LoadFile(path)
}

// LoadFile reads a config from an explicit path.
func LoadFile(path string) (*Config, bool, error) {
	data, err := os.ReadFile(path)

	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, err
	}
	cfg := Default()
	cfg.ReadPaths = nil // user file is authoritative, even when empty
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject unknown keys — a typo must not widen the jail

	if err := dec.Decode(cfg); err != nil {
		return nil, false, fmt.Errorf("parsing %s: %w", path, err)
	}

	if cfg.Version == 0 {
		cfg.Version = 1
	}

	if cfg.Agents == nil {
		cfg.Agents = map[string]bool{}
	}

	if cfg.CustomAgents == nil {
		cfg.CustomAgents = []CustomAgent{}
	}

	return cfg, true, nil
}

// Save writes the config next to its expected location.
func (c *Config) Save() error {
	path, err := Path()

	if err != nil {
		return err
	}

	return c.SaveFile(path)
}

// SaveFile writes the config to an explicit path, creating parents.
func (c *Config) SaveFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(c)

	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

// Resolved is the canonical, absolute view of a Config for one machine.
type Resolved struct {
	Home                string
	ConfigPath          string
	Workspaces          []string
	AllowWorkspaceRead  bool
	AllowWorkspaceWrite bool
	ReadPaths           []string
	WritePaths          []string
	DenyReadPaths       []string
	Agents              map[string]bool
	CustomAgents        []CustomAgent
}

// Resolve expands "~" and canonicalises every path.
func (c *Config) Resolve() (*Resolved, error) {
	home, err := os.UserHomeDir()

	if err != nil {
		return nil, err
	}

	home, err = filepath.EvalSymlinks(home)

	if err != nil {
		return nil, fmt.Errorf("resolving home directory: %w", err)
	}

	cfgPath, _ := Path()

	r := &Resolved{
		Home:                home,
		ConfigPath:          cfgPath,
		AllowWorkspaceRead:  c.AllowWorkspaceRead,
		AllowWorkspaceWrite: c.AllowWorkspaceWrite,
		Agents:              c.Agents,
	}
	canonical := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, p := range in {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			out = append(out, Canonical(p, home))
		}
		return out
	}
	r.Workspaces = canonical(c.Workspaces)
	r.ReadPaths = canonical(c.ReadPaths)
	r.WritePaths = canonical(c.WritePaths)
	r.DenyReadPaths = canonical(c.DenyReadPaths)

	for _, ca := range c.CustomAgents {
		ca.RWPaths = canonical(ca.RWPaths)
		r.CustomAgents = append(r.CustomAgents, ca)
	}

	return r, nil
}

// ExpandHome expands a leading "~" to home.
func ExpandHome(path, home string) string {
	if path == "~" {
		return home
	}

	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}

	return path
}

// Canonical returns the absolute, symlink-resolved form of path. Only the
// longest existing prefix is resolved, so paths that do not exist yet still
// produce the canonical location Seatbelt will see once they do.
func Canonical(path, home string) string {
	p := ExpandHome(path, home)

	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}

	var tail []string
	dir := p
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(dir)

		if parent == dir {
			return filepath.Clean(p)
		}

		tail = append(tail, filepath.Base(dir))
		dir = parent
	}
}

// Within reports whether path is root or lives inside it, boundary-aware
// ("/a/b" is within "/a/b" but "/a/bc" is not within "/a/b").
func Within(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)

	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// Project resolves the write target for cwd: the cwd itself when it lives
// inside a configured workspace, or /private/tmp otherwise. The matched
// workspace is returned for status reporting. atRoot reports whether cwd is
// the workspace root itself — granting a subpath write there would expose
// every project, so the profile limits it to direct children instead.
func (r *Resolved) Project(cwd string) (project, workspace string, inWorkspace, atRoot bool) {
	cwd = Canonical(cwd, r.Home)

	for _, ws := range r.Workspaces {
		if Within(ws, cwd) {
			return cwd, ws, true, cwd == ws
		}
	}

	return "/private/tmp", "", false, false
}

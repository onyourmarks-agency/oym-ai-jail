// Package agents describes the AI CLI tools the jail can wrap.
//
// Each agent declares the extra filesystem access its binary needs (config
// dirs, caches, credential files) on top of what every jailed process gets,
// plus optional arguments that must precede the user's own arguments.
package agents

import (
	"os"
	"regexp"
	"sort"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/config"
)

// Rule is extra filesystem access for one agent. Paths may start with "~";
// regexes may use the %HOME% token (expanded to the escaped home dir).
type Rule struct {
	RWSubpaths []string // directories: read + write
	RWRegexes  []string // Seatbelt regex patterns
}

// Agent is a jailable CLI tool.
type Agent struct {
	Name    string // key in config.agents and the wrapper name
	Command string // executable looked up on PATH (defaults to Name)
	Args    []string
	RW      Rule
	// ExtraPaths returns additional RW subpaths derived from the environment.
	ExtraPaths func() []string
}

// Presets returns the built-in agents in display order.
func Presets() []Agent {
	return []Agent{
		{
			Name: "claude",
			RW: Rule{
				RWSubpaths: []string{"~/.claude", "~/Library/Caches/claude-cli-nodejs"},
				RWRegexes:  []string{"^%HOME%/\\.claude\\..*"},
			},
		},
		{
			Name: "vibe",
			RW: Rule{
				RWSubpaths: []string{"~/.vibe", "~/.mistral"},
			},
		},
		{
			// Codex applies its own Seatbelt profile on macOS, and Seatbelt
			// profiles do not nest: inside our outer jail its sandbox_apply
			// fails with "Operation not permitted". Disabling Codex' inner
			// sandbox leaves our outer jail as the single boundary.
			Name: "codex",
			Args: []string{"--sandbox", "danger-full-access"},
			RW:   Rule{RWSubpaths: []string{"~/.codex"}},
			ExtraPaths: func() []string {
				if dir := os.Getenv("CODEX_HOME"); dir != "" {
					return []string{dir}
				}
				return nil
			},
		},
	}
}

// PresetNames returns the preset names in order.
func PresetNames() []string {
	presets := Presets()
	names := make([]string, len(presets))
	for i, p := range presets {
		names[i] = p.Name
	}
	return names
}

// Lookup finds a runnable agent by name: preset (when enabled) or custom agent.
func Lookup(cfg *config.Resolved, name string) (Agent, bool) {
	if enabled, ok := cfg.Agents[name]; ok {
		if !enabled {
			return Agent{}, false
		}
		for _, p := range Presets() {
			if p.Name == name {
				if p.Command == "" {
					p.Command = p.Name
				}
				return p, true
			}
		}
	}
	for _, ca := range cfg.CustomAgents {
		if ca.Name == name {
			command := ca.Command
			if command == "" {
				command = ca.Name
			}
			return Agent{
				Name:    name,
				Command: command,
				Args:    ca.Args,
				RW:      Rule{RWSubpaths: ca.RWPaths},
			}, true
		}
	}
	return Agent{}, false
}

// Enabled returns every enabled preset plus all custom agents, deduplicated
// by name.
func Enabled(cfg *config.Resolved) []Agent {
	names := make([]string, 0, len(cfg.Agents)+len(cfg.CustomAgents))
	for name, on := range cfg.Agents {
		if on {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	for _, ca := range cfg.CustomAgents {
		names = append(names, ca.Name)
	}

	var out []Agent
	seen := map[string]bool{}

	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if a, ok := Lookup(cfg, name); ok {
			out = append(out, a)
		}
	}
	return out
}

// Access resolves a rule against the machine: "~" becomes home, paths are
// canonicalised and %HOME% in regexes becomes the escaped home dir. It
// copies every slice so the agent's own rule is never mutated.
func (a Agent) Access(home string) Rule {
	var r Rule
	subpaths := append([]string{}, a.RW.RWSubpaths...)

	if a.ExtraPaths != nil {
		subpaths = append(subpaths, a.ExtraPaths()...)
	}

	for _, p := range subpaths {
		r.RWSubpaths = append(r.RWSubpaths, config.Canonical(p, home))
	}

	for _, rx := range a.RW.RWRegexes {
		r.RWRegexes = append(r.RWRegexes,
			regexp.MustCompile(`%HOME%`).ReplaceAllString(rx, regexp.QuoteMeta(home)))
	}

	return r
}

// CommandOrName returns the executable to look up on PATH.
func (a Agent) CommandOrName() string {
	if a.Command != "" {
		return a.Command
	}

	return a.Name
}

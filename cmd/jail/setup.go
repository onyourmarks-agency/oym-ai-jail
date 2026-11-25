package jail

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/agents"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/config"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/zshrc"
)

const (
	modeStrict = "strict"
	modeRead   = "read"
	modeWrite  = "write"
)

func newSetupCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "setup",
		Short: "Configure the jail (same as running oym-ai-jail without arguments).",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if yes {
				return runSetupNonInteractive()
			}
			return runTUI()
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "non-interactive: write detected defaults and wire ~/.zshrc")
	return c
}

func runSetupNonInteractive() error {
	cfg, found, err := config.Load()
	if err != nil {
		return err
	}
	if !found {
		cfg = config.Default()
		for _, name := range agents.PresetNames() {
			if _, err := exec.LookPath(name); err == nil {
				cfg.Agents[name] = true
			}
		}
		if len(cfg.Workspaces) == 0 {
			cfg.Workspaces = detectWorkspaces()
		}
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	path, _ := config.Path()
	fmt.Printf("wrote %s\n", path)
	return finishSetup()
}

// detectWorkspaces guesses sensible project roots from existing directories.
func detectWorkspaces() []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, candidate := range []string{"dev", "Development", "Projects", "code", "Sites", "src"} {
		dir := filepath.Join(home, candidate)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out = append(out, "~/"+candidate)
		}
	}
	if len(out) == 0 {
		out = []string{"~/dev"}
	}
	return out
}

func isolationMode(cfg *config.Config) string {
	switch {
	case cfg.AllowWorkspaceWrite:
		return modeWrite
	case cfg.AllowWorkspaceRead:
		return modeRead
	default:
		return modeStrict
	}
}

func runTUI() error {
	cfg, found, err := config.Load()
	if err != nil {
		return err
	}

	if !found {
		cfg = config.Default()
	}

	firstRun := !found

	presets := agents.Presets()
	selected := make([]string, 0, len(presets))
	for _, p := range presets {
		if on, ok := cfg.Agents[p.Name]; ok {
			if on {
				selected = append(selected, p.Name)
			}
			continue
		}
		// First run: preselect agents that are installed.
		if _, err := exec.LookPath(p.CommandOrName()); err == nil {
			selected = append(selected, p.Name)
		}
	}

	workspaces := cfg.Workspaces

	if len(workspaces) == 0 {
		workspaces = detectWorkspaces()
	}

	mode := isolationMode(cfg)
	workspacesText := strings.Join(workspaces, "\n")
	readText := strings.Join(cfg.ReadPaths, "\n")
	writeText := strings.Join(cfg.WritePaths, "\n")

	zshrcPath, _ := zshrc.Path()
	legacy, _ := zshrc.HasLegacy(zshrcPath)
	removeLegacy := false

	title := "OYM AI Jail — configure"

	if firstRun {
		title = "OYM AI Jail — first-time setup"
	}

	description := "Jails AI agents in the macOS Seatbelt sandbox.\n" +
		"Workspaces: your project roots (one per line).\n" +
		"The agent writes only in the directory you start it from."

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewNote().Title(title).Description(description),
			huh.NewMultiSelect[string]().
				Title("Agents to jail").
				Options(agentOptions(presets, selected)...).
				Value(&selected),
			huh.NewSelect[string]().
				Title("Project isolation").
				Description("strict: only the current directory is visible.\n"+
					"read: sibling projects are readable, current dir writable.\n"+
					"write: the whole workspace is writable.").
				Options(
					huh.NewOption("strict — only current dir", modeStrict).Selected(mode == modeStrict),
					huh.NewOption("read — workspace readable (recommended)", modeRead).Selected(mode == modeRead),
					huh.NewOption("write — workspace writable", modeWrite).Selected(mode == modeWrite),
				).
				Value(&mode),
		),
		huh.NewGroup(
			huh.NewText().
				Title("Workspaces (one per line, ~ allowed)").
				Description(
					"Your project roots — directories that hold your work, like ~/dev.\n"+
						"Starting an agent inside a workspace gives it read access there;\n"+
						"the directory you start it from becomes writable.\n"+
						"Sibling projects stay invisible, readable or writable,\n"+
						"depending on the isolation mode you picked above.").
				Value(&workspacesText).
				Validate(validatePathList(true)),
			huh.NewText().
				Title("Extra read-only paths (one per line)").
				Description(
					"Directories an agent may read but never modify — reference\n"+
						"material, shared docs, a ~/ai-allowed folder you drop files in.\n"+
						"These stay read-only even inside a writable workspace: handy\n"+
						"for things the agent can look at but must not touch.").
				Value(&readText).
				Validate(validatePathList(false)),
			huh.NewText().
				Title("Extra read+write paths (one per line)").
				Description(
					"Directories outside your workspaces the agent may freely\n"+
						"modify — a shared scratch folder, a sync directory,\n"+
						"project templates it generates. Use sparingly: everything\n"+
						"here is as writable as the project itself.").
				Value(&writeText).
				Validate(validatePathList(false)),
		),
	}

	if legacy {
		groups = append(groups, huh.NewGroup(
			huh.NewConfirm().
				Title("Old ai-shell wrappers found in ~/.zshrc").
				Description("Remove the manual claude()/vibe() functions and banner?").
				Affirmative("Remove").
				Negative("Keep").
				Value(&removeLegacy),
		))
	}

	if err := huh.NewForm(groups...).Run(); err != nil {
		return err
	}

	for _, p := range presets {
		cfg.Agents[p.Name] = slices.Contains(selected, p.Name)
	}

	cfg.AllowWorkspaceRead = mode != modeStrict
	cfg.AllowWorkspaceWrite = mode == modeWrite
	cfg.Workspaces = parseLines(workspacesText)
	cfg.ReadPaths = parseLines(readText)
	cfg.WritePaths = parseLines(writeText)

	if err := cfg.Save(); err != nil {
		return err
	}

	path, _ := config.Path()
	fmt.Printf("wrote %s\n", path)

	if removeLegacy {
		if ok, err := zshrc.RemoveLegacy(zshrcPath); err != nil {
			return err
		} else if ok {
			fmt.Println("removed old ai-shell wrappers from ~/.zshrc")
		}
	}
	return finishSetup()
}

func agentOptions(presets []agents.Agent, selected []string) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(presets))
	for _, p := range presets {
		label := p.Name
		if _, err := exec.LookPath(p.CommandOrName()); err != nil {
			label += " (not installed)"
		}
		options = append(options, huh.NewOption(label, p.Name).Selected(slices.Contains(selected, p.Name)))
	}

	return options
}

// validatePathList rejects relative paths: Seatbelt only understands
// absolute or ~ paths, so anything else would silently resolve against cwd.
func validatePathList(required bool) func(string) error {
	return func(s string) error {
		lines := parseLines(s)
		if required && len(lines) == 0 {
			return fmt.Errorf("add at least one path, e.g. ~/dev")
		}

		for _, p := range lines {
			if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, "/") {
				return fmt.Errorf("%q must start with ~ or /", p)
			}
		}

		return nil
	}
}

func parseLines(text string) []string {
	var out []string

	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}

	return out
}

// finishSetup wires ~/.zshrc and creates the extra read paths.
func finishSetup() error {
	path, err := zshrc.Path()
	if err != nil {
		return err
	}

	changed, err := zshrc.Ensure(path)

	if err != nil {
		return err
	}

	cfg, found, err := config.Load()

	if err != nil {
		return err
	}

	if found {
		home, _ := os.UserHomeDir()
		for _, p := range cfg.ReadPaths {
			dir := config.ExpandHome(p, home)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("creating %s: %w", dir, err)
			}
		}
	}

	if changed {
		fmt.Println("wired ~/.zshrc — restart your shell (or run: exec zsh) to activate the jail")
	} else {
		fmt.Println("~/.zshrc already wired")
	}

	fmt.Println("done — check `oym-ai-jail status` from a project directory")

	return nil
}

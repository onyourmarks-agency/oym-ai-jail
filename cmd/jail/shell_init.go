package jail

import (
	"fmt"
	"os"
	"os/exec"
	"sort"

	"github.com/spf13/cobra"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/agents"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/config"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/zshrc"
)

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell-init",
		Short: "Print the zsh wrapper functions for the enabled agents.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, found, err := config.Load()
			if err != nil {
				// Fail closed: a broken config must never leave agents
				// unwrapped and running unjailed. Peek leniently so custom
				// agents are covered too, not just the presets.
				fmt.Print(zshrc.BlockingInit(
					append(agents.PresetNames(), config.PeekAgentNames()...), err))
				return nil
			}
			var names []string
			if !found {
				// No config yet: wrap detected presets so running one explains
				// that setup is required instead of silently bypassing the jail.
				for _, name := range agents.PresetNames() {
					if _, err := exec.LookPath(name); err == nil {
						names = append(names, name)
					}
				}
			} else {
				rc, err := cfg.Resolve()
				if err != nil {
					fmt.Print(zshrc.BlockingInit(
						append(agents.PresetNames(), config.PeekAgentNames()...), err))
					return nil
				}
				for _, a := range agents.Enabled(rc) {
					names = append(names, a.Name)
				}
				sort.Strings(names)
			}
			for _, n := range names {
				if !zshrc.ValidName(n) {
					fmt.Fprintf(os.Stderr, "oym-ai-jail: skipping invalid agent name %q\n", n)
				}
			}
			fmt.Print(zshrc.ShellInit(names))
			return nil
		},
	}
}

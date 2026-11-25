package jail

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/agents"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/sandbox"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run AGENT [ARGS...]",
		Short: "Run an enabled agent inside the jail (used by the zsh wrappers).",
		// ARGS must reach the agent untouched, flags included.
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("usage: oym-ai-jail run AGENT [ARGS...]")
			}

			rc, err := loadResolved()

			if err != nil {
				return err
			}

			agent, ok := agents.Lookup(rc, args[0])

			if !ok {
				return fmt.Errorf("agent %q is not enabled — enable it with `oym-ai-jail`", args[0])
			}

			bin, err := exec.LookPath(agent.Command)

			if err != nil {
				return fmt.Errorf("agent %q not found on PATH: %w", agent.Command, err)
			}

			if resolved, err := filepath.EvalSymlinks(bin); err == nil {
				bin = resolved
			}
			cwd, err := os.Getwd()

			if err != nil {
				return err
			}

			self, _ := selfPath()
			argv := append([]string{bin}, append(agent.Args, args[1:]...)...)

			// Only this agent's support dirs are opened — other enabled
			// agents keep their config hidden from it.
			return sandbox.Run(sandbox.Build(rc, []agents.Agent{agent}, cwd, self), argv)
		},
	}
}

// selfPath resolves the running binary for the self-protection deny rule.
func selfPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
		if resolved, lookErr := exec.LookPath(self); lookErr == nil {
			self = resolved
		}
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		return resolved, nil
	}
	return self, nil
}

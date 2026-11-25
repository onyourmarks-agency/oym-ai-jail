package jail

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/agents"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/sandbox"
)

func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Open an interactive zsh inside the jail (like the old ai-shell).",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			rc, err := loadResolved()

			if err != nil {
				return err
			}

			cwd, err := os.Getwd()

			if err != nil {
				return err
			}

			project, workspace, inWorkspace, _ := rc.Project(cwd)

			if stdinIsTerminal() {
				switch {
				case inWorkspace:
					fmt.Printf("oym-ai-jail active | write: %s | workspace: %s\n", project, workspace)
				default:
					fmt.Printf("oym-ai-jail active | %s is outside your workspaces — invisible, tmp only\n", cwd)
				}
			}

			self, _ := selfPath()
			profile := sandbox.Build(rc, agents.Enabled(rc), cwd, self)

			return sandbox.Run(profile, []string{"/bin/zsh", "-l"})
		},
	}
}

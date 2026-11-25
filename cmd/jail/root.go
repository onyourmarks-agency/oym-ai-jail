// Package jail wires the oym-ai-jail CLI.
package jail

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/buildinfo"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/config"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/selfupdate"
)

// New builds the root command.
func New() *cobra.Command {
	root := &cobra.Command{
		Use:           "oym-ai-jail",
		Short:         "Jail AI coding agents in a macOS Seatbelt sandbox.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !stdinIsTerminal() {
				return cmd.Help()
			}
			return runTUI()
		},
	}
	root.AddCommand(
		newRunCmd(),
		newShellCmd(),
		newShellInitCmd(),
		newStatusCmd(),
		newPrintProfileCmd(),
		newSetupCmd(),
		newUpdateCmd(),
		newUninstallCmd(),
		newVersionCmd(),
	)
	return root
}

func stdinIsTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd())
}

// loadResolved loads and resolves the config, erroring with a hint when the
// user never ran the setup.
func loadResolved() (*config.Resolved, error) {
	cfg, found, err := config.Load()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("no config yet — run `oym-ai-jail` to set up the jail")
	}
	return cfg.Resolve()
}

// updateHint prints a one-line notice when a newer release exists.
func updateHint() {
	if tag := selfupdate.NewerAvailable(buildinfo.Version); tag != "" {
		fmt.Fprintf(os.Stderr, "note: oym-ai-jail %s is available — run `oym-ai-jail update`\n", tag)
	}
}

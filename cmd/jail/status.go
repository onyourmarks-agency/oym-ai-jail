package jail

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/agents"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/sandbox"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/zshrc"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the effective jail for the current directory.",
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

			fmt.Printf("config:     %s\n", rc.ConfigPath)
			fmt.Printf("cwd:        %s\n", cwd)

			if inWorkspace {
				fmt.Printf("workspace:  %s\n", workspace)
				fmt.Printf("write dir:  %s\n", project)
			} else {
				fmt.Println("workspace:  none — cwd is outside all workspaces (invisible, tmp only)")
			}

			mode := "strict (workspace invisible)"

			switch {
			case rc.AllowWorkspaceWrite:
				mode = "write (whole workspace writable)"
			case rc.AllowWorkspaceRead:
				mode = "read (workspace readable, current dir writable)"
			}

			fmt.Printf("isolation:  %s\n", mode)

			if len(rc.ReadPaths) > 0 {
				fmt.Printf("read paths: %s\n", strings.Join(rc.ReadPaths, ", "))
			}

			if len(rc.WritePaths) > 0 {
				fmt.Printf("write paths: %s\n", strings.Join(rc.WritePaths, ", "))
			}

			fmt.Println("agents:")
			for _, name := range agents.PresetNames() {
				if on := rc.Agents[name]; !on {
					fmt.Printf("  %-8s disabled\n", name)
					continue
				}

				bin, err := exec.LookPath(name)

				if err != nil {
					fmt.Printf("  %-8s enabled, NOT on PATH\n", name)
					continue
				}

				fmt.Printf("  %-8s enabled (%s)\n", name, bin)
			}

			for _, ca := range rc.CustomAgents {
				command := ca.Command

				if command == "" {
					command = ca.Name
				}

				fmt.Printf("  %-8s custom (command: %s)\n", ca.Name, command)
			}

			zshrcPath, _ := zshrc.Path()
			ok, _ := zshrc.HasBlock(zshrcPath)

			wired := "not wired — run `oym-ai-jail setup`"

			if ok {
				wired = "wired"
			}

			fmt.Printf("zshrc:      %s\n", wired)
			return nil
		},
	}
}

func newPrintProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "print-profile",
		Short: "Print the Seatbelt profile that would apply in this directory.",
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

			self, _ := selfPath()
			fmt.Print(sandbox.Build(rc, agents.Enabled(rc), cwd, self))
			return nil
		},
	}
}

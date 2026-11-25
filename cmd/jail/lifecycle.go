package jail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/onyourmarks-agency/oym-ai-jail/internal/buildinfo"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/config"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/selfupdate"
	"github.com/onyourmarks-agency/oym-ai-jail/internal/zshrc"
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Self-update to the latest GitHub release.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			return selfupdate.Run(ctx, buildinfo.Version)
		},
	}
}

func newUninstallCmd() *cobra.Command {
	var all, yes bool
	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the zshrc wiring; optionally also the binary and config.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path, _ := zshrc.Path()
			removed, err := zshrc.Remove(path)
			if err != nil {
				return err
			}
			if removed {
				fmt.Println("removed oym-ai-jail block from ~/.zshrc — restart your shell")
			} else {
				fmt.Println("~/.zshrc had no oym-ai-jail block")
			}
			if !all {
				self, _ := selfPath()
				cfgPath, _ := config.Path()
				fmt.Printf("kept binary (%s) and config (%s) — pass --all to remove those too\n", self, cfgPath)
				return nil
			}
			if !yes && stdinIsTerminal() {
				confirm := false
				err := huh.NewConfirm().
					Title("Remove the oym-ai-jail binary and ~/.config/oym-ai-jail?").
					Value(&confirm).
					Run()
				if err != nil {
					return err
				}
				if !confirm {
					fmt.Println("kept binary and config")
					return nil
				}
			} else if !yes {
				return fmt.Errorf("refusing to remove files without a terminal — re-run with --yes")
			}
			if cfgPath, err := config.Path(); err == nil {
				if err := os.RemoveAll(filepath.Dir(cfgPath)); err != nil {
					return err
				}
				fmt.Printf("removed %s\n", filepath.Dir(cfgPath))
			}
			if self, err := selfPath(); err == nil {
				if err := os.Remove(self); err != nil {
					return fmt.Errorf("removing %s: %w", self, err)
				}
				fmt.Printf("removed %s\n", self)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "also remove the binary and the config directory")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information.",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("oym-ai-jail %s (commit %s, built %s)\n",
				buildinfo.Version, buildinfo.GitCommit, buildinfo.BuildTime)
			updateHint()
		},
	}
}

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/config"
)

func (a *app) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect the configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the config file path",
			Args:  cobra.NoArgs,
			Run: func(*cobra.Command, []string) {
				fmt.Println(config.Path())
			},
		},
		&cobra.Command{
			Use:   "show",
			Short: "Print the effective configuration as JSON",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(a.cfg)
			},
		},
		&cobra.Command{
			Use:   "init",
			Short: "Write a config file with the default values",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				path, err := config.WriteDefault()
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("%s already exists", path)
				}
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "wrote %s\n", path)
				return nil
			},
		},
	)
	return cmd
}

func configLocation(a *app) string {
	if p := a.cfg.SourcePath(); p != "" {
		return p
	}
	return config.Path() + " (not created; using defaults)"
}

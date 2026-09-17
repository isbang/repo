package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

func (a *app) newVersionCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Long: `Print the version.

A newer release is normally announced by the background process, at most once a
day; --check asks GitHub right now instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Printf("repo %s (%s %s/%s)\n", a.versionString(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
			if !check {
				return nil
			}
			return a.checkVersionNow(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "ask GitHub whether a newer release exists")
	return cmd
}

// versionString prefers the build-time version, falling back to the module
// version recorded by `go install`.
func (a *app) versionString() string {
	if a.version != "" && a.version != "dev" {
		return a.version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

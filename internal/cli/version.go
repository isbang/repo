package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

func (a *app) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			fmt.Printf("repo %s (%s %s/%s)\n", a.versionString(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}
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

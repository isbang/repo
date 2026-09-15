// Command repo lists the GitHub repositories you can reach, fuzzy-searches them
// and clones the one you pick. The repository list is cached in the XDG cache
// directory and refreshed in the background on every run.
package main

import (
	"os"

	"github.com/isbang/repo/internal/cli"
)

// version is overridden at build time with -ldflags "-X main.version=x.y.z".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}

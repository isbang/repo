//go:build !windows

package selfupdate

import "os"

// replace swaps the new binary into place. Renaming over a running executable
// is safe here: the running process keeps the file it started from, and the
// next run picks up the new one.
func replace(target, binary string) error {
	return os.Rename(binary, target)
}

//go:build windows

package selfupdate

import "os"

// replace swaps the new binary into place. Windows refuses to overwrite a
// running executable, so the old one is moved aside first; it cannot be
// deleted while it runs, and is cleaned up by the next upgrade.
func replace(target, binary string) error {
	backup := target + ".old"
	_ = os.Remove(backup)

	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(binary, target); err != nil {
		_ = os.Rename(backup, target) // put the old one back
		return err
	}
	_ = os.Remove(backup)
	return nil
}

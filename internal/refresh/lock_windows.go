//go:build windows

package refresh

import (
	"os"
	"syscall"
	"time"
)

// staleLock is how long a lock file is honoured before being considered
// abandoned (Windows has no advisory locking here, so we fall back to a
// create-exclusive lock file).
const staleLock = 5 * time.Minute

func tryLock(path string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		return func() {
			f.Close()
			os.Remove(path)
		}, true, nil
	}
	if st, serr := os.Stat(path); serr == nil && time.Since(st.ModTime()) > staleLock {
		if rerr := os.Remove(path); rerr == nil {
			return tryLock(path)
		}
	}
	return nil, false, nil
}

func detachAttr() *syscall.SysProcAttr {
	const detachedProcess = 0x00000008
	return &syscall.SysProcAttr{CreationFlags: detachedProcess}
}

// Package selfupdate replaces the running binary with the build published in a
// GitHub release.
//
// Only release assets are installed, and only after their SHA-256 matches the
// checksums file published alongside them.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/isbang/repo/internal/ghapi"
)

const (
	// ChecksumsName is the release asset listing the SHA-256 of every archive.
	ChecksumsName = "checksums.txt"

	// maxArchive and maxChecksums bound what is read from the network, so a
	// wrong or hostile URL cannot fill the disk.
	maxArchive   = 200 << 20
	maxChecksums = 1 << 20

	downloadTimeout = 5 * time.Minute
)

var (
	// ErrNoAsset means the release has no build for this OS and architecture.
	ErrNoAsset = errors.New("no release asset for this platform")
	// ErrNoChecksum means the release publishes no checksum for the asset, so
	// there is nothing to verify the download against.
	ErrNoChecksum = errors.New("no checksum published for this asset")
	// ErrNotWritable means the binary cannot be replaced where it is installed.
	ErrNotWritable = errors.New("binary is not writable")
)

// Target resolves the binary an upgrade would replace: the running executable
// with symlinks followed, so an upgrade through a symlinked ~/.local/bin/repo
// rewrites the real file rather than replacing the link.
func Target() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil // unresolvable, but the path itself may still work
	}
	return resolved, nil
}

// Writable reports whether target can be replaced, by creating the temporary
// file the upgrade would use. Checking the binary itself is no good: a running
// executable refuses to open for writing even where a rename would work.
func Writable(target string) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".repo-upgrade-*")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotWritable, filepath.Dir(target), err)
	}
	tmp.Close()
	return os.Remove(tmp.Name())
}

// Find returns the release asset built for goos/goarch. Assets are matched on
// the "_<goos>_<goarch>." infix rather than a full name, so renaming the
// archives does not break upgrades.
func Find(rel ghapi.Release, goos, goarch string) (ghapi.Asset, error) {
	infix := fmt.Sprintf("_%s_%s.", goos, goarch)
	for _, a := range rel.Assets {
		if a.Name == ChecksumsName || !strings.Contains(a.Name, infix) {
			continue
		}
		if strings.HasSuffix(a.Name, ".tar.gz") {
			return a, nil
		}
	}
	return ghapi.Asset{}, fmt.Errorf("%w (%s/%s) in %s", ErrNoAsset, goos, goarch, rel.TagName)
}

// Apply downloads the asset for this platform, checks it against the release
// checksums and swaps it into place at target.
//
// progress, if non-nil, is called with the bytes downloaded so far and the
// expected total.
func Apply(ctx context.Context, rel ghapi.Release, target string, progress func(done, total int64)) error {
	asset, err := Find(rel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := Writable(target); err != nil {
		return err
	}

	want, err := checksumFor(ctx, rel, asset.Name)
	if err != nil {
		return err
	}

	dir := filepath.Dir(target)
	archive, err := download(ctx, asset.URL, dir, asset.Size, progress)
	if err != nil {
		return err
	}
	defer os.Remove(archive)

	if err := verify(archive, want); err != nil {
		return err
	}

	binary, err := extract(archive, dir, filepath.Base(target))
	if err != nil {
		return err
	}
	defer os.Remove(binary) // no-op once the replace succeeds

	if err := copyMode(target, binary); err != nil {
		return err
	}
	return replace(target, binary)
}

// checksumFor returns the published SHA-256 of one asset.
func checksumFor(ctx context.Context, rel ghapi.Release, name string) (string, error) {
	var sums ghapi.Asset
	for _, a := range rel.Assets {
		if a.Name == ChecksumsName {
			sums = a
			break
		}
	}
	if sums.URL == "" {
		return "", fmt.Errorf("%w: %s is missing from %s", ErrNoChecksum, ChecksumsName, rel.TagName)
	}

	body, err := get(ctx, sums.URL)
	if err != nil {
		return "", err
	}
	defer body.Close()

	b, err := io.ReadAll(io.LimitReader(body, maxChecksums))
	if err != nil {
		return "", err
	}
	sum := ParseChecksums(string(b))[name]
	if sum == "" {
		return "", fmt.Errorf("%w: %s is not listed in %s", ErrNoChecksum, name, ChecksumsName)
	}
	return sum, nil
}

// ParseChecksums reads the `sha256sum` format: one "<hex>  <name>" per line.
func ParseChecksums(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		// The name may carry sha256sum's binary-mode "*" marker.
		out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return out
}

// download writes url into a temporary file next to the target binary, so the
// eventual rename stays on one filesystem.
func download(ctx context.Context, url, dir string, size int64, progress func(done, total int64)) (string, error) {
	body, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer body.Close()

	tmp, err := os.CreateTemp(dir, ".repo-download-*")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	var src io.Reader = io.LimitReader(body, maxArchive)
	if progress != nil {
		src = &progressReader{r: src, total: size, report: progress}
	}
	if _, err := io.Copy(tmp, src); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "isbang-repo-cli")

	resp, err := (&http.Client{Timeout: downloadTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

func verify(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

// extract writes the binary inside a .tar.gz archive to a temporary file in
// dir. The entry named like the installed binary wins; failing that, the only
// regular file in the archive does.
func extract(archive, dir, name string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filepath.Base(archive), err)
	}
	defer gz.Close()

	var (
		out       *os.File
		found     string
		ambiguous bool
	)
	defer func() {
		if out != nil {
			out.Close()
		}
	}()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		base := filepath.Base(hdr.Name)
		exact := base == name || base == name+".exe"
		if found != "" && !exact {
			ambiguous = true
			continue
		}
		if found != "" && exact {
			continue // an exact match already won
		}

		out, err = os.CreateTemp(dir, ".repo-binary-*")
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, maxArchive)); err != nil {
			os.Remove(out.Name())
			return "", err
		}
		found = out.Name()
		out.Close()
		out = nil

		if exact {
			return found, nil
		}
	}

	if found == "" {
		return "", fmt.Errorf("no binary inside %s", filepath.Base(archive))
	}
	if ambiguous {
		os.Remove(found)
		return "", fmt.Errorf("no file named %q inside %s", name, filepath.Base(archive))
	}
	return found, nil
}

// copyMode gives the new binary the permissions of the one it replaces, so an
// upgrade never widens or narrows access. A missing target means a fresh
// install: 0755.
func copyMode(target, binary string) error {
	mode := os.FileMode(0o755)
	if st, err := os.Stat(target); err == nil {
		mode = st.Mode().Perm()
	}
	return os.Chmod(binary, mode)
}

// progressReader reports how much of the body has arrived.
type progressReader struct {
	r      io.Reader
	total  int64
	done   int64
	report func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	p.report(p.done, p.total)
	return n, err
}

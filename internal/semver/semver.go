// Package semver compares the version strings this CLI meets: release tags like
// "v1.2.3", the "dev" placeholder of an untagged build, and the module version
// `go install` records.
//
// It implements just the ordering rules of https://semver.org — the numeric
// core, then pre-release precedence. Build metadata is ignored, as the spec
// requires.
package semver

import (
	"cmp"
	"strconv"
	"strings"
)

// Version is a parsed version. Missing components are zero, so "v1" and "1.0.0"
// compare equal.
type Version struct {
	core [3]int
	pre  string
}

// Prerelease is the pre-release suffix without its leading "-", empty for a
// final release.
func (v Version) Prerelease() string { return v.pre }

// Parse reads a version, tolerating a leading "v" and build metadata. It
// reports ok=false for anything whose core is not dotted numbers, which is how
// "dev", "(devel)" and commit shas are rejected.
func Parse(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}

	var v Version
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre, s = s[i+1:], s[:i]
	}

	fields := strings.Split(s, ".")
	if len(fields) > len(v.core) {
		return Version{}, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 || strings.ContainsAny(f, "+-") {
			return Version{}, false
		}
		v.core[i] = n
	}
	return v, true
}

// Valid reports whether s is a version this package can compare.
func Valid(s string) bool {
	_, ok := Parse(s)
	return ok
}

// Compare returns -1 when v sorts before w, +1 when after, and 0 when the two
// have equal precedence.
func (v Version) Compare(w Version) int {
	for i := range v.core {
		if c := cmp.Compare(v.core[i], w.core[i]); c != 0 {
			return c
		}
	}
	return comparePre(v.pre, w.pre)
}

// Newer reports whether latest is strictly newer than current. It is false
// unless both parse: an untagged build has nothing to be newer than.
func Newer(latest, current string) bool {
	l, ok := Parse(latest)
	if !ok {
		return false
	}
	c, ok := Parse(current)
	if !ok {
		return false
	}
	return l.Compare(c) > 0
}

// comparePre implements pre-release precedence: a final release outranks any
// pre-release of the same core, numeric identifiers compare numerically and
// rank below alphanumeric ones, and a longer run of otherwise equal
// identifiers wins.
func comparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}

	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := compareIdent(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

func compareIdent(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return cmp.Compare(an, bn)
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	default:
		return cmp.Compare(a, b)
	}
}

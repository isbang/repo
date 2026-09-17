package semver

import "testing"

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "dev", "(devel)", "2b6854c", "v", "1.2.3.4", "1..2", "1.x", "va.b.c"} {
		if v, ok := Parse(s); ok {
			t.Errorf("Parse(%q) = %v, true; want ok=false", s, v)
		}
	}
}

func TestParseAccepts(t *testing.T) {
	tests := []struct {
		in   string
		core [3]int
		pre  string
	}{
		{"v1.2.3", [3]int{1, 2, 3}, ""},
		{"1.2.3", [3]int{1, 2, 3}, ""},
		{"v1", [3]int{1, 0, 0}, ""},
		{"v1.2", [3]int{1, 2, 0}, ""},
		{" v0.0.1 ", [3]int{0, 0, 1}, ""},
		{"v1.2.3-rc.1", [3]int{1, 2, 3}, "rc.1"},
		{"v1.2.3+build.5", [3]int{1, 2, 3}, ""},
		{"v1.2.3-rc.1+build.5", [3]int{1, 2, 3}, "rc.1"},
	}
	for _, tt := range tests {
		v, ok := Parse(tt.in)
		if !ok {
			t.Errorf("Parse(%q) ok = false, want true", tt.in)
			continue
		}
		if v.core != tt.core || v.Prerelease() != tt.pre {
			t.Errorf("Parse(%q) = %v/%q, want %v/%q", tt.in, v.core, v.Prerelease(), tt.core, tt.pre)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"v1", "v1.0.0", 0},
		{"v1.0.0+a", "v1.0.0+b", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.1.0", "v1.0.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"v0.9.0", "v0.10.0", -1},
		// A pre-release sorts below its own release, and above the previous one.
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0-rc.1", "v0.9.9", 1},
		{"v1.0.0-alpha", "v1.0.0-beta", -1},
		{"v1.0.0-rc.2", "v1.0.0-rc.10", -1},
		{"v1.0.0-rc", "v1.0.0-rc.1", -1},
		{"v1.0.0-1", "v1.0.0-alpha", -1},
	}
	for _, tt := range tests {
		a, ok := Parse(tt.a)
		if !ok {
			t.Fatalf("Parse(%q) failed", tt.a)
		}
		b, ok := Parse(tt.b)
		if !ok {
			t.Fatalf("Parse(%q) failed", tt.b)
		}
		if got := a.Compare(b); got != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		if got := b.Compare(a); got != -tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.b, tt.a, got, -tt.want)
		}
	}
}

func TestNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"v1.1.0", "v1.0.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v1.1.0", false},
		{"v1.0.0-rc.1", "v1.0.0", false},
		// An unversioned build never triggers a notice in either direction.
		{"v1.0.0", "dev", false},
		{"", "v1.0.0", false},
		{"nightly", "v1.0.0", false},
	}
	for _, tt := range tests {
		if got := Newer(tt.latest, tt.current); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

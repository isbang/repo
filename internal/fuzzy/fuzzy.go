// Package fuzzy implements fzf-style fuzzy matching with positional scoring.
//
// A pattern matches when its characters appear in order (not necessarily
// adjacent) in the text. Scoring rewards matches that start at word boundaries
// ("/", "-", "_", ".", camelCase humps) and that run consecutively, and
// penalises gaps — so "kt" ranks "kube-tools" above "knight".
//
// Matching is case-insensitive unless the pattern contains an uppercase letter
// (smart case). Space-separated terms are ANDed: every term must match.
package fuzzy

import (
	"sort"
	"strings"
	"unicode"
)

// Scoring weights. Tuned to behave like fzf on repository names.
const (
	scoreMatch       = 16
	scoreConsecutive = 8
	bonusBoundary    = 8
	bonusCamel       = 6
	// The character after a "/" starts the repository name in "owner/name",
	// which is what people usually mean; it outranks even the first character
	// of the string (bonusBoundary + bonusFirstChar).
	bonusSlash       = 16
	bonusFirstChar   = 4
	penaltyGapStart  = -3
	penaltyGapExtend = -1
)

const negInf = -1 << 30

// Match scores pattern against text. ok is false when the pattern does not match.
func Match(pattern, text string) (score int, ok bool) {
	terms := strings.Fields(pattern)
	if len(terms) == 0 {
		return 0, true
	}
	m := newMatcher(text)
	total := 0
	for _, term := range terms {
		s, ok := m.score([]rune(term), hasUpper(term))
		if !ok {
			return 0, false
		}
		total += s
	}
	return total, true
}

// MatchPositions scores pattern against text and reports the matched rune
// indices, for highlighting. The positions are a valid match of the pattern but
// not necessarily the exact path the score was computed from.
func MatchPositions(pattern, text string) (score int, positions []int, ok bool) {
	terms := strings.Fields(pattern)
	if len(terms) == 0 {
		return 0, nil, true
	}
	m := newMatcher(text)
	total := 0
	seen := map[int]struct{}{}
	for _, term := range terms {
		p := []rune(term)
		cs := hasUpper(term)
		s, ok := m.score(p, cs)
		if !ok {
			return 0, nil, false
		}
		total += s
		for _, pos := range m.positions(p, cs) {
			seen[pos] = struct{}{}
		}
	}
	positions = make([]int, 0, len(seen))
	for pos := range seen {
		positions = append(positions, pos)
	}
	sort.Ints(positions)
	return total, positions, true
}

// matcher caches the per-text data shared by every term of a pattern.
type matcher struct {
	text  []rune
	lower []rune
	bonus []int
}

func newMatcher(text string) *matcher {
	t := []rune(text)
	m := &matcher{
		text:  t,
		lower: make([]rune, len(t)),
		bonus: make([]int, len(t)),
	}
	for i, r := range t {
		m.lower[i] = unicode.ToLower(r)
	}
	for i, r := range t {
		if i == 0 {
			m.bonus[i] = bonusBoundary + bonusFirstChar
			continue
		}
		prev := t[i-1]
		switch {
		case prev == '/':
			m.bonus[i] = bonusSlash
		case isDelimiter(prev):
			m.bonus[i] = bonusBoundary
		case unicode.IsLower(prev) && unicode.IsUpper(r):
			m.bonus[i] = bonusCamel
		case unicode.IsDigit(r) && !unicode.IsDigit(prev):
			m.bonus[i] = bonusCamel
		}
	}
	return m
}

func (m *matcher) eq(i int, r rune, caseSensitive bool) bool {
	if caseSensitive {
		return m.text[i] == r
	}
	return m.lower[i] == unicode.ToLower(r)
}

// score runs an affine-gap dynamic program over the text.
//
// M[i][j] is the best score for matching p[0..i] with p[i] landing exactly on
// text[j]. G[i][j] is the best score for having matched p[0..i] and being ready
// to match p[i+1] at j with at least one skipped character before j — that is
// where the gap penalties accumulate.
func (m *matcher) score(p []rune, caseSensitive bool) (int, bool) {
	n := len(m.text)
	if len(p) == 0 {
		return 0, true
	}
	if len(p) > n || !m.subsequence(p, caseSensitive) {
		return 0, false
	}

	prevM, prevG := make([]int, n), make([]int, n)
	curM, curG := make([]int, n), make([]int, n)

	for i := range p {
		for j := 0; j < n; j++ {
			curM[j] = negInf
			if !m.eq(j, p[i], caseSensitive) {
				continue
			}
			base := negInf
			switch {
			case i == 0:
				base = 0 // the first pattern character may start anywhere
			case j == 0:
				// nothing can precede it
			default:
				if prevM[j-1] > negInf {
					base = prevM[j-1] + scoreConsecutive
				}
				if prevG[j] > base {
					base = prevG[j]
				}
			}
			if base == negInf {
				continue
			}
			curM[j] = base + scoreMatch + m.bonus[j]
		}

		for j := 0; j < n; j++ {
			g := negInf
			if j >= 1 && curG[j-1] > negInf {
				g = curG[j-1] + penaltyGapExtend
			}
			if j >= 2 && curM[j-2] > negInf {
				if s := curM[j-2] + penaltyGapStart; s > g {
					g = s
				}
			}
			curG[j] = g
		}

		prevM, curM = curM, prevM
		prevG, curG = curG, prevG
	}

	best := negInf
	for _, s := range prevM {
		if s > best {
			best = s
		}
	}
	if best == negInf {
		return 0, false
	}
	return best, true
}

// subsequence is a cheap pre-filter: most repositories fail here, which keeps
// the dynamic program off the hot path.
func (m *matcher) subsequence(p []rune, caseSensitive bool) bool {
	i := 0
	for j := 0; j < len(m.text) && i < len(p); j++ {
		if m.eq(j, p[i], caseSensitive) {
			i++
		}
	}
	return i == len(p)
}

// positions greedily matches the pattern from every possible starting point and
// keeps the best-scoring run, which is what a reader expects to see highlighted.
func (m *matcher) positions(p []rune, caseSensitive bool) []int {
	var (
		best      []int
		bestScore = negInf
	)
	for start := 0; start+len(p) <= len(m.text); start++ {
		if !m.eq(start, p[0], caseSensitive) {
			continue
		}
		pos := make([]int, 0, len(p))
		score, j, prev := 0, start, -1
		for i := 0; i < len(p); i++ {
			for j < len(m.text) && !m.eq(j, p[i], caseSensitive) {
				j++
			}
			if j == len(m.text) {
				pos = nil
				break
			}
			s := scoreMatch + m.bonus[j]
			switch {
			case i == 0:
			case j == prev+1:
				s += scoreConsecutive
			default:
				s += penaltyGapStart + penaltyGapExtend*(j-prev-2)
			}
			score += s
			pos = append(pos, j)
			prev = j
			j++
		}
		if pos != nil && score > bestScore {
			bestScore, best = score, pos
		}
	}
	return best
}

func isDelimiter(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

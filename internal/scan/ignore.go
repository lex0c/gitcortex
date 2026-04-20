package scan

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// IgnoreRule is one line of the ignore file, pre-parsed.
// Supported syntax (a subset of gitignore, enough for directory pruning):
//
//	# comment                (ignored)
//	node_modules             (literal name — matches as basename or full path)
//	vendor/                  (directory-only; matches dirs named vendor)
//	archive/*                (glob — forwarded to path.Match on the rel path)
//	**/generated             (deep-match: same as `generated` matched anywhere)
//	!important               (negation — re-includes a match)
//
// Matching is evaluated in order; the last rule to match wins, following
// gitignore semantics so `!foo` after `foo/` re-includes foo.
type IgnoreRule struct {
	Pattern string
	Negate  bool
	DirOnly bool
}

type Matcher struct {
	rules []IgnoreRule
}

func NewMatcher(patterns []string) *Matcher {
	m := &Matcher{}
	for _, p := range patterns {
		if r, ok := parseRule(p); ok {
			m.rules = append(m.rules, r)
		}
	}
	return m
}

// LoadMatcher reads an ignore file and returns a matcher. A missing file
// is not an error — scan simply proceeds with no user-provided rules.
// This matches the gitignore contract where `.gitignore` is optional.
func LoadMatcher(file string) (*Matcher, error) {
	f, err := os.Open(file)
	if err != nil {
		if os.IsNotExist(err) {
			return NewMatcher(nil), nil
		}
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		patterns = append(patterns, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return NewMatcher(patterns), nil
}

func parseRule(line string) (IgnoreRule, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return IgnoreRule{}, false
	}
	r := IgnoreRule{}
	if strings.HasPrefix(line, "!") {
		r.Negate = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.DirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	r.Pattern = line
	return r, r.Pattern != ""
}

// Match reports whether relPath should be ignored. isDir hints directory-
// only rules (`vendor/` only fires on dirs).
//
// Evaluation order: scan every rule, track the last match. This is what
// gitignore does — it lets `!src/keep` override a broader earlier `src/`
// block without forcing the user to care about rule ordering beyond the
// obvious "put exceptions after the thing they exclude".
func (m *Matcher) Match(relPath string, isDir bool) bool {
	relPath = filepath.ToSlash(relPath)
	matched := false
	for _, r := range m.rules {
		if r.DirOnly && !isDir {
			continue
		}
		if !matchRule(r, relPath) {
			continue
		}
		matched = !r.Negate
	}
	return matched
}

// CouldReinclude reports whether any negation rule targets a descendant
// of dir — i.e. walking into the dir could still yield a re-included
// path. Callers use this to decide whether to prune an ignored
// directory via filepath.SkipDir or descend into it and evaluate
// children individually.
//
// Without this check the walker short-circuits the matcher's last-
// match-wins semantics: a `vendor/` + `!vendor/keep` pair would skip
// the vendor subtree entirely before vendor/keep could be examined,
// silently dropping the re-included path.
//
// Returns true when:
//   - a negation's pattern has no path separator (basename rules like
//     `!keep` or `!*.go` that could fire at any depth), OR
//   - a negation begins with `**/` (deep-match; applies anywhere in the
//     tree), OR
//   - the pattern's leading path segments are each compatible with the
//     corresponding segment of dir via path.Match, so its trailing
//     segment(s) could name a descendant. This covers literal prefixes
//     like `!vendor/keep` AND globbed prefixes like `!vendor*/keep` or
//     `!*/keep` — both can match children of an ignored `vendor`.
//
// Negations that target a sibling or ancestor path (e.g. `!src/keep`
// when walking vendor) correctly don't trigger descent, so pruning
// remains effective for unrelated ignored trees like `node_modules/`.
func (m *Matcher) CouldReinclude(dir string) bool {
	dir = filepath.ToSlash(dir)
	dirSegs := strings.Split(dir, "/")
	for _, r := range m.rules {
		if !r.Negate {
			continue
		}
		pat := r.Pattern
		// `**/foo` and other deep-match patterns can fire at any
		// depth — walking into any ignored subtree could reach one.
		if strings.HasPrefix(pat, "**/") {
			return true
		}
		// Basename-only rules (no `/`) apply to any segment. `!keep`
		// could re-include vendor/keep, a/b/keep, anywhere.
		if !strings.Contains(pat, "/") {
			return true
		}
		patSegs := strings.Split(pat, "/")
		// The pattern must have strictly more segments than dir;
		// otherwise its deepest named entity is dir itself or an
		// ancestor, never a descendant worth descending for.
		if len(patSegs) <= len(dirSegs) {
			continue
		}
		// Each of the pattern's leading segments must be compatible
		// with the corresponding dir segment. path.Match handles both
		// literal names (`vendor`) and globs (`vendor*`, `*`, `?`)
		// uniformly; a failing match on any segment rules this
		// negation out.
		ok := true
		for i := 0; i < len(dirSegs); i++ {
			matched, err := path.Match(patSegs[i], dirSegs[i])
			if err != nil || !matched {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func matchRule(r IgnoreRule, relPath string) bool {
	pat := r.Pattern

	// **/foo → strip leading **/ and treat as "match foo anywhere" —
	// same as the basename-or-suffix check below. We don't support
	// arbitrary ** in the middle of patterns because users coming from
	// gitignore expect the common forms (prefix dir, basename, ext),
	// not the full double-star algebra. Saves us writing a mini doublestar
	// engine for a marginal feature.
	if strings.HasPrefix(pat, "**/") {
		pat = strings.TrimPrefix(pat, "**/")
	}

	base := path.Base(relPath)

	// Literal basename: `node_modules` matches any segment named that.
	if pat == base {
		return true
	}
	// Any segment of the path equals the pattern (literal dir/file name
	// embedded in the tree). "vendor" matches "a/b/vendor/c.go".
	if !strings.ContainsAny(pat, "*?[") && segmentMatch(pat, relPath) {
		return true
	}
	// Glob on basename: "*.log"
	if matched, _ := path.Match(pat, base); matched {
		return true
	}
	// Glob on full relative path: "archive/*"
	if matched, _ := path.Match(pat, relPath); matched {
		return true
	}
	// Directory prefix: "archive/" (after DirOnly stripping applied above
	// removed the trailing slash) or "archive/*".
	prefix := strings.TrimSuffix(pat, "*")
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" && prefix != pat {
		if strings.HasPrefix(relPath, prefix+"/") || relPath == prefix {
			return true
		}
	}
	return false
}

func segmentMatch(needle, relPath string) bool {
	for _, seg := range strings.Split(relPath, "/") {
		if seg == needle {
			return true
		}
	}
	return false
}


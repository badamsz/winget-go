// Package exclude implements package exclusion lists: patterns of
// packages that bulk operations (upgrade, import) must skip.
//
// A pattern is a case-insensitive glob (*, ?, [...]) matched against the
// package identifier, e.g. "Microsoft.Edge*". A "name:" prefix matches
// the display name instead, e.g. "name:Visual Studio*". Lists are
// separated by commas, semicolons or newlines; "#" starts a comment.
package exclude

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"strings"

	"winget-go/internal/config"
)

type pattern struct {
	raw    string
	glob   string
	byName bool
	origin string
}

// List is a compiled exclusion list. A nil *List excludes nothing.
type List struct {
	patterns []pattern
}

// Split parses a list string into patterns.
func Split(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		for _, p := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ';' }) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// Add validates and appends patterns, recording where they came from.
func (l *List) Add(origin string, patterns ...string) error {
	for _, raw := range patterns {
		p := pattern{raw: raw, origin: origin}
		g := raw
		if rest, ok := strings.CutPrefix(strings.ToLower(raw), "name:"); ok {
			p.byName, g = true, strings.TrimSpace(raw[len(raw)-len(rest):])
		}
		p.glob = strings.ToLower(g)
		if _, err := path.Match(p.glob, ""); err != nil {
			return fmt.Errorf("%s: invalid exclusion pattern %q: %w", origin, raw, err)
		}
		l.patterns = append(l.patterns, p)
	}
	return nil
}

// AddFile reads patterns from a file (one or more per line).
func (l *List) AddFile(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("exclusion file: %w", err)
	}
	defer f.Close()
	var b strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return l.Add(file, Split(b.String())...)
}

// Len reports the number of patterns.
func (l *List) Len() int {
	if l == nil {
		return 0
	}
	return len(l.patterns)
}

// Patterns returns "pattern (origin)" descriptions.
func (l *List) Patterns() []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l.patterns))
	for i, p := range l.patterns {
		out[i] = fmt.Sprintf("%s  (%s)", p.raw, p.origin)
	}
	return out
}

// Match reports whether a package is excluded and by which pattern.
func (l *List) Match(id, name string) (string, bool) {
	if l == nil {
		return "", false
	}
	id, name = strings.ToLower(id), strings.ToLower(name)
	for _, p := range l.patterns {
		target := id
		if p.byName {
			target = name
		}
		if target == "" {
			continue
		}
		if ok, _ := path.Match(p.glob, target); ok {
			return p.raw, true
		}
	}
	return "", false
}

// Options are the per-run exclusion flags.
type Options struct {
	Patterns []string // --exclude (each may itself be a list)
	Files    []string // --exclude-file
	Disable  bool     // --no-exclude
}

// Load builds the effective list: WINGET_GO_EXCLUDE and
// WINGET_GO_EXCLUDE_FILE from the environment and every winget-go.env
// file, plus command-line flags. --no-exclude yields an empty list.
func Load(o Options) (*List, error) {
	l := &List{}
	if o.Disable {
		return l, nil
	}
	for _, v := range config.ListSetting("WINGET_GO_EXCLUDE") {
		if err := l.Add("WINGET_GO_EXCLUDE", Split(v)...); err != nil {
			return nil, err
		}
	}
	files := []string{}
	for _, v := range config.ListSetting("WINGET_GO_EXCLUDE_FILE") {
		files = append(files, splitPaths(v)...)
	}
	files = append(files, o.Files...)
	for _, f := range files {
		if err := l.AddFile(f); err != nil {
			return nil, err
		}
	}
	for _, v := range o.Patterns {
		if err := l.Add("--exclude", Split(v)...); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// Configured reports whether the environment / env files define any
// exclusions (used to steer backend selection before flags are parsed).
func Configured() bool {
	return len(config.ListSetting("WINGET_GO_EXCLUDE")) > 0 || len(config.ListSetting("WINGET_GO_EXCLUDE_FILE")) > 0
}

// splitPaths splits a WINGET_GO_EXCLUDE_FILE value on the OS list
// separator and newlines (not commas, which may appear in paths).
func splitPaths(v string) []string {
	var out []string
	for _, line := range strings.Split(v, "\n") {
		for _, p := range strings.Split(line, string(os.PathListSeparator)) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

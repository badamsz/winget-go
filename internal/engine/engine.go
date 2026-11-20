// Package engine implements winget operations in pure Go (the "portable"
// backend). It is used when no native winget is available, when the
// user forces it, and on non-Windows hosts (where installs are not
// possible but search/show/download are).
package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"winget-go/internal/config"
	"winget-go/internal/exclude"
	"winget-go/internal/installed"
	"winget-go/internal/source"
)

// Engine holds opened sources and output streams.
type Engine struct {
	Sources []*source.Source
	Out     io.Writer
	Err     io.Writer
	In      io.Reader
	// GitHub enables GitHub Releases lookups for GitHub-hosted installers.
	GitHub bool
	// Exclusions lists packages that upgrade/import must skip (nil = none).
	Exclusions *exclude.List

	state *installed.State
}

// New opens the configured sources (or only the one named).
func New(ctx context.Context, only string, out, errw io.Writer) (*Engine, error) {
	cfgs, err := config.LoadSources()
	if err != nil {
		return nil, err
	}
	e := &Engine{Out: out, Err: errw, In: os.Stdin}
	for _, c := range cfgs {
		if only != "" && !strings.EqualFold(c.Name, only) {
			continue
		}
		if only == "" && c.Explicit {
			continue
		}
		s, err := source.Open(ctx, c, errw)
		if err != nil {
			fmt.Fprintf(errw, "warning: source %q unavailable: %v\n", c.Name, err)
			continue
		}
		e.Sources = append(e.Sources, s)
	}
	if len(e.Sources) == 0 {
		if only != "" {
			return nil, fmt.Errorf("no source named %q is available", only)
		}
		return nil, errors.New("no sources are available")
	}
	return e, nil
}

// Close releases sources.
func (e *Engine) Close() {
	for _, s := range e.Sources {
		s.Close()
	}
}

// State lazily loads install records.
func (e *Engine) State() (*installed.State, error) {
	if e.state == nil {
		s, err := installed.Load()
		if err != nil {
			return nil, err
		}
		e.state = s
	}
	return e.state, nil
}

// Search queries every source.
func (e *Engine) Search(q source.Query) ([]source.Result, error) {
	var all []source.Result
	for _, s := range e.Sources {
		r, err := s.Search(q)
		if err != nil {
			return nil, fmt.Errorf("search %s: %w", s.Config.Name, err)
		}
		all = append(all, r...)
	}
	return all, nil
}

// ErrNoMatch / ErrAmbiguous mirror winget's errors.
var (
	ErrNoMatch = errors.New("no package found matching input criteria")
)

// AmbiguousError lists candidates when a query matches several packages.
type AmbiguousError struct{ Results []source.Result }

func (a *AmbiguousError) Error() string {
	var b strings.Builder
	b.WriteString("multiple packages found matching input criteria; please refine the input (e.g. --id <id> --exact):\n")
	for i, r := range a.Results {
		if i == 20 {
			fmt.Fprintf(&b, "  ... and %d more\n", len(a.Results)-20)
			break
		}
		fmt.Fprintf(&b, "  %-40s %s\n", r.Name, r.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Resolve finds exactly one package for a query, preferring an exact
// (case-insensitive) id match like winget does.
func (e *Engine) Resolve(q source.Query) (*source.Package, error) {
	rs, err := e.Search(q)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, ErrNoMatch
	}
	var exactID []source.Result
	for _, r := range rs {
		if strings.EqualFold(r.ID, q.Term) {
			exactID = append(exactID, r)
		}
	}
	switch {
	case len(exactID) == 1:
		return exactID[0].Package, nil
	case len(rs) == 1:
		return rs[0].Package, nil
	}
	// A single exact match on any field also wins.
	var exact []source.Result
	for _, r := range rs {
		if r.MatchType <= source.MatchCaseInsensitive {
			exact = append(exact, r)
		}
	}
	if len(exact) == 1 {
		return exact[0].Package, nil
	}
	return nil, &AmbiguousError{Results: rs}
}

// confirm asks a yes/no question on the terminal.
func (e *Engine) confirm(prompt string) bool {
	if f, ok := e.In.(*os.File); ok {
		if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	fmt.Fprintf(e.Out, "%s [Y/N]: ", prompt)
	line, _ := bufio.NewReader(e.In).ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// Installed lists installed applications correlated with sources.
func (e *Engine) Installed() ([]*installed.Item, error) {
	st, err := e.State()
	if err != nil {
		return nil, err
	}
	return installed.List(st, e.Sources)
}

// HasWildcard reports whether term is a glob pattern (contains *, ? or [).
func HasWildcard(term string) bool { return strings.ContainsAny(term, "*?[") }

// FindInstalled returns installed items matching a query (by package id,
// name, or ARP key).
//
// A term containing *, ? or [ is a case-insensitive glob (path.Match
// syntax) that must match the whole Id or the whole display name, e.g.
// "Microsoft.DotNet.*" or "Python 3.1?.*". Backslashes are literal so
// ARP ids (ARP\HKLM\...) can be matched as written. Otherwise an exact Id match
// wins; failing that, names (with exact) or Id/name substrings match.
func (e *Engine) FindInstalled(term string, exact bool) ([]*installed.Item, error) {
	t := strings.ToLower(term)
	glob := HasWildcard(t)
	if glob {
		t = globPattern(t)
		if _, err := path.Match(t, ""); err != nil {
			return nil, fmt.Errorf("invalid wildcard pattern %q: %w", term, err)
		}
	}
	items, err := e.Installed()
	if err != nil {
		return nil, err
	}
	return matchInstalled(items, t, exact, glob), nil
}

// globPattern lowercases a user pattern and makes backslashes literal.
func globPattern(term string) string {
	return strings.ReplaceAll(strings.ToLower(term), `\`, `\\`)
}

func matchInstalled(items []*installed.Item, t string, exact, glob bool) []*installed.Item {
	var idHits, hits []*installed.Item
	for _, it := range items {
		id := strings.ToLower(it.ID())
		name := strings.ToLower(it.Name)
		if glob {
			idOK, _ := path.Match(t, id)
			nameOK, _ := path.Match(t, name)
			if idOK || nameOK {
				hits = append(hits, it)
			}
			continue
		}
		switch {
		case id == t:
			idHits = append(idHits, it)
		case exact && name == t:
			hits = append(hits, it)
		case !exact && (strings.Contains(id, t) || strings.Contains(name, t)):
			hits = append(hits, it)
		}
	}
	if len(idHits) > 0 {
		return idHits
	}
	return hits
}

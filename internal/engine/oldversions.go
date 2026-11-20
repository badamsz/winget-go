package engine

import (
	"fmt"
	"sort"
	"strings"

	"winget-go/internal/installed"
	"winget-go/internal/source"
	"winget-go/internal/version"
)

// VersionLine says which installed versions count as "the same line" when
// removing old versions.
type VersionLine int

const (
	LineMajor VersionLine = iota // 3.11.4 and 3.12.1 are the same line (3)
	LineMinor                    // 3.11.4 and 3.11.9 are; 3.12.1 is not
	LineAll                      // every version of the package is one line
)

// ParseVersionLine parses "major", "minor" or "all".
func ParseVersionLine(s string) (VersionLine, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "major":
		return LineMajor, nil
	case "minor":
		return LineMinor, nil
	case "all":
		return LineAll, nil
	}
	return 0, fmt.Errorf("invalid --old-versions value %q (use major, minor or all)", s)
}

// key returns the version line for v, or "" if v is not a usable version.
func (l VersionLine) key(v string) string {
	if version.Prefix(v, 1) == "" {
		return ""
	}
	switch l {
	case LineMajor:
		return version.Prefix(v, 1)
	case LineMinor:
		return version.Prefix(v, 2)
	}
	return "*"
}

// OldVersionGroup is one product line with the entries kept and removed.
type OldVersionGroup struct {
	Name    string // display label for the group
	Line    string // version line, e.g. "3" or "3.11" ("*" for all)
	Keep    []*installed.Item
	Remove  []*installed.Item
	Skipped []*installed.Item // no usable version; never removed
}

// productKey decides which installed entries are versions of the same
// product. Matched packages group by Id; otherwise by normalized display
// name (version stripped, architecture kept) and publisher. Scope is part
// of the key so per-user and per-machine installs are never treated as
// old versions of each other.
func productKey(it *installed.Item) string {
	scope := ""
	if it.ARP != nil {
		scope = it.ARP.Scope
	}
	arch := ""
	if k := source.NameKey(it.Name); strings.HasSuffix(k, ")") {
		arch = k[strings.LastIndex(k, "("):]
	}
	if it.Package != nil {
		return "id:" + strings.ToLower(it.Package.ID) + arch + "|" + scope
	}
	return "name:" + source.NameKey(it.Name) + "|" + source.NormalizePublisher(it.Publisher) + "|" + scope
}

// PlanOldVersions groups installed items into product lines and selects,
// in each line, every version older than the newest `keep` versions.
// Entries whose version equals a kept version are never selected, and
// entries without a version are reported as skipped.
func PlanOldVersions(items []*installed.Item, line VersionLine, keep int) []OldVersionGroup {
	if keep < 1 {
		keep = 1
	}
	type bucket struct {
		name  string
		line  string
		items []*installed.Item
		skip  []*installed.Item
	}
	buckets := map[string]*bucket{}
	var order []string
	get := func(k, name, ln string) *bucket {
		b := buckets[k]
		if b == nil {
			b = &bucket{name: name, line: ln}
			buckets[k] = b
			order = append(order, k)
		}
		return b
	}
	for _, it := range items {
		pk := productKey(it)
		v := it.Version
		ln := line.key(v)
		if ln == "" {
			b := get(pk+"#?", it.Name, "?")
			b.skip = append(b.skip, it)
			continue
		}
		b := get(pk+"#"+ln, it.Name, ln)
		b.items = append(b.items, it)
	}
	var out []OldVersionGroup
	for _, k := range order {
		b := buckets[k]
		g := OldVersionGroup{Name: b.name, Line: b.line, Skipped: b.skip}
		sort.SliceStable(b.items, func(i, j int) bool {
			return version.Compare(b.items[i].Version, b.items[j].Version) > 0
		})
		if len(b.items) > 0 {
			g.Name = b.items[0].Name
		}
		for i, it := range b.items {
			if i < keep {
				g.Keep = append(g.Keep, it)
				continue
			}
			// Strictly older than the oldest kept version only.
			if version.Compare(it.Version, g.Keep[len(g.Keep)-1].Version) < 0 {
				g.Remove = append(g.Remove, it)
			} else {
				g.Keep = append(g.Keep, it)
			}
		}
		if len(g.Remove) > 0 {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

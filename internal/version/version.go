// Package version implements winget-style version comparison.
//
// A version is split on '.'; each part is a leading integer followed by an
// optional string remainder. Integers compare numerically; when equal, a
// part with no remainder sorts after one with a remainder (1.0 > 1.0-beta),
// otherwise remainders compare case-insensitively. Trailing zero parts are
// ignored, so 1.0 == 1.0.0.
package version

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type part struct {
	num   uint64
	other string
}

func parse(v string) []part {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if v == "" {
		return nil
	}
	var parts []part
	for _, s := range strings.Split(v, ".") {
		s = strings.TrimSpace(s)
		i := 0
		for i < len(s) && unicode.IsDigit(rune(s[i])) {
			i++
		}
		var n uint64
		if i > 0 {
			n, _ = strconv.ParseUint(s[:i], 10, 64)
		}
		parts = append(parts, part{num: n, other: strings.ToLower(strings.TrimSpace(s[i:]))})
	}
	for len(parts) > 0 {
		last := parts[len(parts)-1]
		if last.num == 0 && last.other == "" {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	return parts
}

// Compare returns -1, 0 or 1. "Unknown" (or empty) sorts lowest and
// "Latest" sorts highest, matching winget's special versions.
func Compare(a, b string) int {
	if ra, rb := rank(a), rank(b); ra != rb {
		if ra < rb {
			return -1
		}
		return 1
	} else if ra != 1 {
		return 0
	}
	pa, pb := parse(a), parse(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y part
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x.num != y.num {
			if x.num < y.num {
				return -1
			}
			return 1
		}
		if x.other == y.other {
			continue
		}
		if x.other == "" {
			return 1
		}
		if y.other == "" {
			return -1
		}
		if x.other < y.other {
			return -1
		}
		return 1
	}
	return 0
}

func rank(v string) int {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "unknown":
		return 0
	case "latest":
		return 2
	}
	return 1
}

// Prefix returns the first n numeric components of v joined by ".",
// padding with zeros: Prefix("3.11.4", 2) == "3.11", Prefix("7", 2) == "7.0".
// It returns "" for empty or unknown versions.
func Prefix(v string, n int) string {
	if rank(v) != 1 {
		return ""
	}
	ps := parse(v)
	if len(ps) == 0 && strings.TrimSpace(v) == "" {
		return ""
	}
	out := make([]string, n)
	for i := range out {
		var x uint64
		if i < len(ps) {
			x = ps[i].num
		}
		out[i] = strconv.FormatUint(x, 10)
	}
	return strings.Join(out, ".")
}

// Less reports whether a < b.
func Less(a, b string) bool { return Compare(a, b) < 0 }

// SortDesc sorts versions newest first.
func SortDesc(vs []string) {
	sort.SliceStable(vs, func(i, j int) bool { return Compare(vs[i], vs[j]) > 0 })
}

package source

import (
	"regexp"
	"strings"
	"unicode"
)

// These approximate winget's NameNormalizer, which the index uses to
// correlate Apps & Features entries (e.g. "Mozilla Firefox 120.0 (x64
// en-US)") with packages ("mozillafirefox(X64)").
var (
	reArch64  = regexp.MustCompile(`(?i)\b(x64|amd64|x86_64|x86-64|64[- ]?bit|win64)\b`)
	reArch32  = regexp.MustCompile(`(?i)\b(x86|i[36]86|32[- ]?bit|win32)\b`)
	reArchArm = regexp.MustCompile(`(?i)\b(arm64|aarch64)\b`)
	reVersion = regexp.MustCompile(`(?i)\bv?\d+(\.\d+)+[\w\-+]*`)
	reLocale  = regexp.MustCompile(`(?i)\b[a-z]{2}(-[a-z]{2,4})?\b(-[a-z]{2})?`)
	reParen   = regexp.MustCompile(`[\(\[][^\)\]]*[\)\]]`)
	reLegal   = regexp.MustCompile(`(?i)\b(inc|incorporated|corp|corporation|co|company|llc|ltd|limited|gmbh|ag|sa|srl|bv|pty|plc|oy|ab|as|kk|s\.?a\.?r\.?l\.?)\b\.?`)
)

func alnum(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizeName returns candidate normalized names for an ARP display
// name, with and without an architecture suffix.
func NormalizeName(name string) []string {
	arch := ""
	switch {
	case reArchArm.MatchString(name):
		arch = "(Arm64)"
	case reArch64.MatchString(name):
		arch = "(X64)"
	case reArch32.MatchString(name):
		arch = "(X86)"
	}
	s := reParen.ReplaceAllString(name, " ")
	s = reArchArm.ReplaceAllString(s, " ")
	s = reArch64.ReplaceAllString(s, " ")
	s = reArch32.ReplaceAllString(s, " ")
	s = reVersion.ReplaceAllString(s, " ")
	base := alnum(s)
	full := alnum(name)
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	add(base)
	if arch != "" {
		add(base + arch)
	}
	add(full)
	// Also try without trailing locale tokens ("Foo de" -> "foo").
	if trimmed := alnum(reLocale.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Contains(m, "-") {
			return " "
		}
		return m
	})); trimmed != base {
		add(trimmed)
		if arch != "" {
			add(trimmed + arch)
		}
	}
	return out
}

// NormalizePublisher strips legal-entity suffixes and punctuation.
func NormalizePublisher(p string) string {
	return alnum(reLegal.ReplaceAllString(p, " "))
}

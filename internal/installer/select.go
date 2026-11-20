// Package installer selects, downloads and runs package installers.
package installer

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"winget-go/internal/manifest"
)

// SelectOptions narrows installer selection.
type SelectOptions struct {
	Architecture  string // x64, x86, arm64, arm, neutral ("" = system default)
	Scope         string // user, machine ("" = any)
	Locale        string // e.g. en-US ("" = system preference)
	InstallerType string // e.g. msi, exe, portable ("" = any)
	PreferMachine bool   // prefer machine scope (e.g. when elevated)
}

// SystemArchitectures returns the architectures this machine can run, in
// preference order. On non-Windows hosts (download-only use) it assumes a
// Windows x64 target.
func SystemArchitectures() []string {
	if runtime.GOOS != "windows" {
		return []string{"x64", "x86", "neutral"}
	}
	switch runtime.GOARCH {
	case "arm64":
		return []string{"arm64", "x64", "x86", "neutral"}
	case "386":
		if os.Getenv("PROCESSOR_ARCHITEW6432") == "AMD64" {
			return []string{"x64", "x86", "neutral"}
		}
		return []string{"x86", "neutral"}
	default:
		return []string{"x64", "x86", "neutral"}
	}
}

// SystemLocale guesses the user's locale from the environment.
func SystemLocale() string {
	for _, k := range []string{"WINGET_GO_LOCALE", "LC_ALL", "LANG"} {
		if v := os.Getenv(k); v != "" && v != "C" && v != "POSIX" {
			v, _, _ = strings.Cut(v, ".")
			return strings.ReplaceAll(v, "_", "-")
		}
	}
	return "en-US"
}

func lang(l string) string {
	l, _, _ = strings.Cut(strings.ToLower(l), "-")
	return l
}

// Select picks the best installer for this system.
func Select(m *manifest.Manifest, o SelectOptions) (*manifest.Installer, error) {
	archs := SystemArchitectures()
	if o.Architecture != "" {
		archs = []string{strings.ToLower(o.Architecture), "neutral"}
	}
	archRank := map[string]int{}
	for i, a := range archs {
		archRank[a] = i
	}
	locale := o.Locale
	if locale == "" {
		locale = SystemLocale()
	}

	type cand struct {
		in    *manifest.Installer
		score [4]int
		order int
	}
	var cands []cand
	var rejected []string
	for i := range m.Installers {
		in := &m.Installers[i]
		ar, ok := archRank[strings.ToLower(in.Architecture)]
		if !ok {
			rejected = append(rejected, in.Architecture+"/"+in.InstallerType)
			continue
		}
		if o.Scope != "" && in.Scope != "" && !strings.EqualFold(in.Scope, o.Scope) {
			continue
		}
		if o.InstallerType != "" && !strings.EqualFold(effectiveType(in), o.InstallerType) && !strings.EqualFold(in.InstallerType, o.InstallerType) {
			continue
		}
		if !desktopPlatform(in.Platform) {
			continue
		}
		if o.Locale != "" && in.InstallerLocale != "" && lang(in.InstallerLocale) != lang(o.Locale) {
			continue
		}
		locRank := 2
		switch {
		case strings.EqualFold(in.InstallerLocale, locale):
			locRank = 0
		case in.InstallerLocale != "" && lang(in.InstallerLocale) == lang(locale):
			locRank = 1
		case in.InstallerLocale == "":
			locRank = 1
		}
		scopeRank := 1
		wantScope := "user"
		if o.PreferMachine {
			wantScope = "machine"
		}
		if in.Scope == "" || strings.EqualFold(in.Scope, wantScope) {
			scopeRank = 0
		}
		storeRank := 0
		if strings.EqualFold(in.InstallerType, "msstore") || in.InstallerUrl == "" {
			storeRank = 1
		}
		cands = append(cands, cand{in: in, score: [4]int{storeRank, ar, locRank, scopeRank}, order: i})
	}
	if len(cands) == 0 {
		msg := "no applicable installer found"
		if len(rejected) > 0 {
			msg += fmt.Sprintf(" for architectures %v (available: %s)", archs, strings.Join(rejected, ", "))
		}
		return nil, fmt.Errorf("%s; try --architecture, --scope, --locale or --installer-type", msg)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i].score, cands[j].score
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return cands[i].order < cands[j].order
	})
	best := cands[0].in
	if best.InstallerUrl == "" {
		return nil, fmt.Errorf("the only applicable installer is from the Microsoft Store (%s), which winget-go cannot install", best.InstallerType)
	}
	return best, nil
}

func desktopPlatform(p []string) bool {
	if len(p) == 0 {
		return true
	}
	for _, v := range p {
		if strings.EqualFold(v, "Windows.Desktop") {
			return true
		}
	}
	return false
}

// effectiveType is the installer type that actually runs (the nested type
// for archives).
func effectiveType(in *manifest.Installer) string {
	if strings.EqualFold(in.InstallerType, "zip") && in.NestedInstallerType != "" {
		return strings.ToLower(in.NestedInstallerType)
	}
	return strings.ToLower(in.InstallerType)
}

// Extension returns a sensible file extension for a downloaded installer.
func Extension(in *manifest.Installer) string {
	u := in.InstallerUrl
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "."); i > strings.LastIndex(u, "/") && len(u)-i <= 9 {
		ext := strings.ToLower(u[i:])
		switch ext {
		case ".exe", ".msi", ".msix", ".msixbundle", ".appx", ".appxbundle", ".zip":
			return ext
		}
	}
	switch strings.ToLower(in.InstallerType) {
	case "msi", "wix":
		return ".msi"
	case "msix", "appx":
		return ".msix"
	case "zip":
		return ".zip"
	}
	return ".exe"
}

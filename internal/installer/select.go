// Package installer selects, downloads and runs package installers.
package installer

import (
	"fmt"
	"os"
	"runtime"
	"slices"
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
	// InstalledType is the technology of the copy being upgraded (msi,
	// msix, exe, portable, …; "" = unknown). Unless InstallerType is set,
	// only installers of a compatible technology are considered, because
	// e.g. an MSIX does not replace an MSI install; it lands beside it.
	InstalledType string
}

// TechFamily groups installer types that upgrade each other in place:
// "msi" (msi, wix), "msix" (msix, appx), "portable" (portable, zip with
// portable files) and "exe" (exe, inno, nullsoft, burn). Unknown types
// return "".
func TechFamily(t string) string {
	switch strings.ToLower(t) {
	case "msi", "wix":
		return "msi"
	case "msix", "appx":
		return "msix"
	case "portable", "zip/portable":
		return "portable"
	case "exe", "inno", "nullsoft", "burn":
		return "exe"
	}
	return ""
}

// TechMismatchError means newer installers exist, but only for a
// different install technology than the installed copy.
type TechMismatchError struct {
	Installed string   // technology family of the installed copy
	Available []string // installer types that were applicable otherwise
}

func (e *TechMismatchError) Error() string {
	return fmt.Sprintf("the new version is only available as %s, but the installed copy is a %s install; "+
		"pass --migrate to uninstall it and install the new version, or --installer-type %s to install the new version alongside it",
		strings.Join(e.Available, "/"), strings.ToUpper(e.Installed), e.Available[0])
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
	reject := func(in *manifest.Installer, why string) {
		rejected = append(rejected, fmt.Sprintf("%s/%s (%s)", in.Architecture, effectiveType(in), why))
	}
	installedFamily := ""
	if o.InstallerType == "" {
		installedFamily = TechFamily(o.InstalledType)
	}
	var otherTech []string
	for i := range m.Installers {
		in := &m.Installers[i]
		ar, ok := archRank[strings.ToLower(in.Architecture)]
		if !ok {
			reject(in, "architecture")
			continue
		}
		if o.Scope != "" && in.Scope != "" && !strings.EqualFold(in.Scope, o.Scope) {
			reject(in, "scope "+in.Scope)
			continue
		}
		if o.InstallerType != "" && !strings.EqualFold(effectiveType(in), o.InstallerType) && !strings.EqualFold(in.InstallerType, o.InstallerType) {
			reject(in, "installer type")
			continue
		}
		if !windowsPlatform(in.Platform) {
			reject(in, "platform "+strings.Join(in.Platform, ","))
			continue
		}
		if o.Locale != "" && in.InstallerLocale != "" && lang(in.InstallerLocale) != lang(o.Locale) {
			reject(in, "locale "+in.InstallerLocale)
			continue
		}
		if installedFamily != "" {
			if f := TechFamily(effectiveType(in)); f != "" && f != installedFamily {
				if t := effectiveType(in); !slices.Contains(otherTech, t) {
					otherTech = append(otherTech, t)
				}
				continue
			}
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
		if len(otherTech) > 0 {
			return nil, &TechMismatchError{Installed: installedFamily, Available: otherTech}
		}
		msg := "no applicable installer found"
		if len(rejected) > 0 {
			msg += fmt.Sprintf(" for %s; rejected: %s", strings.Join(archs, "/"), strings.Join(rejected, ", "))
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

// windowsPlatform reports whether an installer targets desktop Windows.
// Windows.Universal (UWP/MSIX) packages run on desktop too, as in winget;
// only an explicit list without either value (e.g. a future platform) is
// rejected.
func windowsPlatform(p []string) bool {
	if len(p) == 0 {
		return true
	}
	for _, v := range p {
		if strings.EqualFold(v, "Windows.Desktop") || strings.EqualFold(v, "Windows.Universal") {
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

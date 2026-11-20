// Package platform isolates OS-specific operations: process launch with
// optional UAC elevation, Apps & Features (ARP) enumeration, PATH edits
// and locating a native winget. Non-Windows builds return ErrUnsupported
// for anything that only makes sense on Windows.
package platform

import (
	"encoding/json"
	"errors"
	"runtime"
	"strings"
)

// ErrUnsupported is returned by Windows-only operations elsewhere.
var ErrUnsupported = errors.New("this operation requires Windows (installers are Windows programs); use `download` instead")

// IsWindows reports whether we run on Windows.
func IsWindows() bool { return runtime.GOOS == "windows" }

// ARPEntry is one Apps & Features (Uninstall registry) entry.
type ARPEntry struct {
	Key                  string // unique, winget's format: Machine\X64\<keyname>, Machine\X86\…, User\X64\…
	KeyName              string // e.g. {GUID} for MSI products
	DisplayName          string
	DisplayVersion       string
	Publisher            string
	UninstallString      string
	QuietUninstallString string
	InstallLocation      string
	WindowsInstaller     bool
	Scope                string // user | machine
}

// LegacyARPKey converts a key written by earlier winget-go builds
// (HKLM\…, HKLM32\…, HKCU\…) to the current format. Other keys are
// returned unchanged.
func LegacyARPKey(k string) string {
	for old, cur := range map[string]string{`HKLM\`: `Machine\X64\`, `HKLM32\`: `Machine\X86\`, `HKCU\`: `User\X64\`} {
		if len(k) > len(old) && strings.EqualFold(k[:len(old)], old) {
			return cur + k[len(old):]
		}
	}
	return k
}

// MSIXPackage is an installed MSIX/AppX package registered for the
// current user (what Get-AppxPackage returns).
type MSIXPackage struct {
	Name            string // package name, e.g. Microsoft.WindowsAppRuntime.1.8
	FullName        string // Name_Version_Arch_ResourceId_PublisherId
	FamilyName      string // Name_PublisherId
	Version         string
	Architecture    string // x64, x86, arm64, neutral
	Publisher       string // certificate subject (CN=…)
	InstallLocation string
	IsFramework     bool
	NonRemovable    bool
	SignatureKind   string // Store, Developer, System, Enterprise, None

	DisplayName          string // resolved from the manifest, or Name
	PublisherDisplayName string // resolved from the manifest, or the CN
}

// ParseAppxJSON decodes the JSON emitted by the Get-AppxPackage script.
func ParseAppxJSON(b []byte) ([]MSIXPackage, error) {
	b = []byte(strings.TrimSpace(strings.TrimPrefix(string(b), "\uFEFF")))
	if len(b) == 0 {
		return nil, nil
	}
	var pkgs []MSIXPackage
	if b[0] == '{' { // a single object if the array wrapper got lost
		var one MSIXPackage
		if err := json.Unmarshal(b, &one); err != nil {
			return nil, err
		}
		pkgs = []MSIXPackage{one}
	} else if err := json.Unmarshal(b, &pkgs); err != nil {
		return nil, err
	}
	for i := range pkgs {
		p := &pkgs[i]
		p.Architecture = strings.ToLower(p.Architecture)
		if p.DisplayName == "" {
			p.DisplayName = p.Name
		}
		if p.PublisherDisplayName == "" {
			p.PublisherDisplayName = SubjectName(p.Publisher)
		}
	}
	return pkgs, nil
}

// SubjectName returns the CN (or O) of a certificate subject such as
// "CN=Microsoft Corporation, O=Microsoft Corporation, C=US".
func SubjectName(subject string) string {
	var o string
	for _, part := range splitRDN(subject) {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.ToUpper(k) {
		case "CN":
			return v
		case "O":
			o = v
		}
	}
	if o != "" {
		return o
	}
	return subject
}

// splitRDN splits a certificate subject on commas outside double quotes.
func splitRDN(s string) []string {
	var parts []string
	inQuote, start := false, 0
	for i, c := range s {
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ',' && !inQuote:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// MSResourceURI turns a manifest string reference ("ms-resource:AppName",
// "ms-resource:/Res/AppName", "ms-resource://Pkg/Res/AppName") into the
// full URI SHLoadIndirectString expects. It returns "" for literals.
func MSResourceURI(pkgName, ref string) string {
	const pfx = "ms-resource:"
	if len(ref) < len(pfx) || !strings.EqualFold(ref[:len(pfx)], pfx) {
		return ""
	}
	r := ref[len(pfx):]
	switch {
	case strings.HasPrefix(r, "//"):
		return pfx + r
	case strings.HasPrefix(r, "/"):
		return pfx + "//" + pkgName + r
	case strings.Contains(r, "/"):
		return pfx + "//" + pkgName + "/" + r
	}
	return pfx + "//" + pkgName + "/Resources/" + r
}

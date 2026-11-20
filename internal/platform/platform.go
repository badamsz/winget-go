// Package platform isolates OS-specific operations: process launch with
// optional UAC elevation, Apps & Features (ARP) enumeration, PATH edits
// and locating a native winget. Non-Windows builds return ErrUnsupported
// for anything that only makes sense on Windows.
package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"runtime"
	"strconv"
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

// MSIX deployment HRESULTs that get special handling.
const (
	HResultPackageNotFound   = 0x80073CF1 // ERROR_INSTALL_PACKAGE_NOT_FOUND
	HResultDependencyFailure = 0x80073CF3 // ERROR_INSTALL_RESOLVE_DEPENDENCY_FAILED
	HResultAccessDenied      = 0x80070005 // E_ACCESSDENIED
)

// MSIXError is a failed MSIX deployment operation with a readable message.
type MSIXError struct {
	HResult uint32
	Message string   // cleaned PowerShell/deployment message
	UsedBy  []string // for a framework in use: the packages depending on it
}

func (e *MSIXError) Error() string {
	switch {
	case len(e.UsedBy) > 0:
		return fmt.Sprintf("this framework is in use by %s; Windows removes a framework automatically once nothing depends on it",
			strings.Join(e.UsedBy, ", "))
	case e.HResult == HResultAccessDenied:
		return "access denied; run from an elevated prompt (0x80070005)"
	case e.HResult != 0 && !strings.Contains(e.Message, fmt.Sprintf("%08X", e.HResult)):
		return fmt.Sprintf("%s (0x%08X)", e.Message, e.HResult)
	}
	return e.Message
}

var (
	clixmlError = regexp.MustCompile(`(?s)<S S="Error">(.*?)</S>`)
	clixmlEsc   = regexp.MustCompile(`_x([0-9A-Fa-f]{4})_`)
	psHResult   = regexp.MustCompile(`HRESULT:\s*0x([0-9A-Fa-f]{8})`)
	psUsedBy    = regexp.MustCompile(`(?s)because package\(s\)\s+(.+?)\s+currently depends? on the framework`)
	spaces      = regexp.MustCompile(`\s+`)
)

// DecodeCLIXML extracts the error text from PowerShell's serialized
// stderr ("#< CLIXML" followed by <Objs>), dropping progress records.
// Other input is returned unchanged.
func DecodeCLIXML(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#< CLIXML") {
		return s
	}
	var b strings.Builder
	for _, m := range clixmlError.FindAllStringSubmatch(s, -1) {
		b.WriteString(html.UnescapeString(m[1]))
	}
	return clixmlEsc.ReplaceAllStringFunc(b.String(), func(x string) string {
		n, _ := strconv.ParseUint(x[2:6], 16, 16)
		return string(rune(n))
	})
}

// ParseMSIXError turns the output of a failed Remove-AppxPackage (plain
// text or CLIXML) into an MSIXError: the HRESULT, the deployment message
// without PowerShell's script position/category noise, and for a
// framework in use the list of dependent packages.
func ParseMSIXError(out string, hresult uint32) *MSIXError {
	msg := DecodeCLIXML(out)
	if i := strings.Index(msg, "NOTE: For additional information"); i >= 0 {
		msg = msg[:i]
	}
	if i := strings.Index(msg, "\nAt line:"); i >= 0 {
		msg = msg[:i]
	}
	msg = strings.TrimSpace(spaces.ReplaceAllString(msg, " "))
	msg = strings.TrimPrefix(msg, "Remove-AppxPackage : ")
	e := &MSIXError{HResult: hresult, Message: msg}
	if e.HResult == 0 {
		if m := psHResult.FindStringSubmatch(msg); m != nil {
			n, _ := strconv.ParseUint(m[1], 16, 32)
			e.HResult = uint32(n)
		}
	}
	if m := psUsedBy.FindStringSubmatch(msg); m != nil {
		e.UsedBy = strings.Fields(m[1])
	}
	if e.Message == "" {
		e.Message = "Remove-AppxPackage failed"
	}
	return e
}

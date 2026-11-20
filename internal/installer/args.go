package installer

import (
	"strings"

	"winget-go/internal/manifest"
)

// Mode is the installer UI mode.
type Mode int

const (
	ModeSilentWithProgress Mode = iota // winget's default
	ModeSilent
	ModeInteractive
)

// ArgOptions controls command-line construction.
type ArgOptions struct {
	Mode            Mode
	Scope           string
	InstallLocation string
	LogPath         string
	Override        string // replaces all arguments
	Custom          string // appended
	Upgrade         bool
}

// defaultSwitches mirrors winget's built-in switches per installer type.
func defaultSwitches(t string) manifest.Switches {
	switch t {
	case "msi", "wix":
		return manifest.Switches{
			Silent:             "/quiet /norestart",
			SilentWithProgress: "/passive /norestart",
			Log:                `/log "<LOGPATH>"`,
			InstallLocation:    `TARGETDIR="<INSTALLPATH>" INSTALLDIR="<INSTALLPATH>"`,
		}
	case "burn":
		return manifest.Switches{
			Silent:             "/quiet /norestart",
			SilentWithProgress: "/passive /norestart",
			Log:                `/log "<LOGPATH>"`,
		}
	case "nullsoft":
		return manifest.Switches{
			Silent:             "/S",
			SilentWithProgress: "/S",
			InstallLocation:    "/D=<INSTALLPATH>",
		}
	case "inno":
		return manifest.Switches{
			Silent:             "/SP- /VERYSILENT /SUPPRESSMSGBOXES /NORESTART",
			SilentWithProgress: "/SP- /SILENT /SUPPRESSMSGBOXES /NORESTART",
			Log:                `/LOG="<LOGPATH>"`,
			InstallLocation:    `/DIR="<INSTALLPATH>"`,
		}
	}
	return manifest.Switches{}
}

// MergedSwitches returns the manifest switches with type defaults filling
// any gaps.
func MergedSwitches(in *manifest.Installer) manifest.Switches {
	s := in.InstallerSwitches
	d := defaultSwitches(effectiveType(in))
	fill := func(dst *string, def string) {
		if *dst == "" {
			*dst = def
		}
	}
	fill(&s.Silent, d.Silent)
	fill(&s.SilentWithProgress, d.SilentWithProgress)
	fill(&s.Log, d.Log)
	fill(&s.InstallLocation, d.InstallLocation)
	return s
}

// BuildArgs builds the installer's argument string (for exe-style and
// msiexec-style installers) from the manifest and user options.
func BuildArgs(in *manifest.Installer, o ArgOptions) string {
	if o.Override != "" {
		return o.Override
	}
	t := effectiveType(in)
	s := MergedSwitches(in)
	var parts []string
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			parts = append(parts, v)
		}
	}
	switch o.Mode {
	case ModeSilent:
		add(s.Silent)
	case ModeSilentWithProgress:
		add(s.SilentWithProgress)
	case ModeInteractive:
		add(s.Interactive)
	}
	if o.LogPath != "" && s.Log != "" {
		add(strings.ReplaceAll(s.Log, "<LOGPATH>", o.LogPath))
	}
	switch t {
	case "inno":
		switch strings.ToLower(o.Scope) {
		case "user":
			add("/CURRENTUSER")
		case "machine":
			add("/ALLUSERS")
		}
	case "msi", "wix":
		switch strings.ToLower(o.Scope) {
		case "user":
			add("ALLUSERS=2 MSIINSTALLPERUSER=1")
		case "machine":
			add("ALLUSERS=1")
		}
	}
	if o.Upgrade {
		add(s.Upgrade)
	}
	add(s.Custom)
	add(o.Custom)
	// NSIS requires /D= to be the last argument.
	if o.InstallLocation != "" && s.InstallLocation != "" {
		add(strings.ReplaceAll(s.InstallLocation, "<INSTALLPATH>", o.InstallLocation))
	}
	return strings.Join(parts, " ")
}

// IsSuccess reports whether an exit code means success, and whether a
// reboot is required.
func IsSuccess(in *manifest.Installer, code int64) (ok, reboot bool) {
	switch code {
	case 0:
		return true, false
	case 3010, 1641: // ERROR_SUCCESS_REBOOT_REQUIRED / _INITIATED
		return true, true
	}
	for _, c := range in.InstallerSuccessCodes {
		if c == code {
			return true, false
		}
	}
	return false, false
}

// DescribeExitCode explains a failing exit code using the manifest's
// ExpectedReturnCodes.
func DescribeExitCode(in *manifest.Installer, code int64) string {
	for _, e := range in.ExpectedReturnCodes {
		if e.InstallerReturnCode == code {
			msg := responseText(e.ReturnResponse)
			if e.ReturnResponseUrl != "" {
				msg += " (" + e.ReturnResponseUrl + ")"
			}
			return msg
		}
	}
	switch code {
	case 1602:
		return "the installation was cancelled by the user"
	case 1618:
		return "another installation is already in progress"
	case 1603:
		return "fatal error during installation"
	case 1625:
		return "the installation is forbidden by system policy (try an elevated prompt)"
	}
	return ""
}

func responseText(r string) string {
	m := map[string]string{
		"packageInUse":              "the package is in use; close the application and retry",
		"installInProgress":         "another installation is already in progress",
		"fileInUse":                 "a file is in use; close applications and retry",
		"missingDependency":         "a dependency is missing",
		"diskFull":                  "the disk is full",
		"insufficientMemory":        "insufficient memory",
		"noNetwork":                 "a network connection is required",
		"contactSupport":            "contact the publisher's support",
		"rebootRequiredToFinish":    "a reboot is required to finish installation",
		"rebootRequiredForInstall":  "a reboot is required before installing",
		"rebootInitiated":           "the installer initiated a reboot",
		"cancelledByUser":           "the installation was cancelled",
		"alreadyInstalled":          "the package is already installed",
		"downgrade":                 "a newer version is already installed",
		"blockedByPolicy":           "the installation is blocked by policy",
		"systemNotSupported":        "this system is not supported",
		"invalidParameter":          "the installer received an invalid parameter",
		"packageInUseByApplication": "the package is in use by another application",
	}
	if v, ok := m[r]; ok {
		return v
	}
	return r
}

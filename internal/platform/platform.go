// Package platform isolates OS-specific operations: process launch with
// optional UAC elevation, Apps & Features (ARP) enumeration, PATH edits
// and locating a native winget. Non-Windows builds return ErrUnsupported
// for anything that only makes sense on Windows.
package platform

import (
	"errors"
	"runtime"
)

// ErrUnsupported is returned by Windows-only operations elsewhere.
var ErrUnsupported = errors.New("this operation requires Windows (installers are Windows programs); use `download` instead")

// IsWindows reports whether we run on Windows.
func IsWindows() bool { return runtime.GOOS == "windows" }

// ARPEntry is one Apps & Features (Uninstall registry) entry.
type ARPEntry struct {
	Key                  string // unique: <hive>\<view>\<keyname>
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

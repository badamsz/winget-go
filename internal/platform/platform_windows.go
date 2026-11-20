//go:build windows

package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// IsElevated reports whether the process token is elevated.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// Run starts exe with a raw argument string (passed verbatim, so switches
// like /D=C:\Program Files\X keep their exact form) and waits for it.
// With elevate set (or when Windows reports ERROR_ELEVATION_REQUIRED) it
// relaunches through UAC.
func Run(ctx context.Context, exe, args string, elevate bool) (int64, error) {
	if elevate && !IsElevated() {
		return runElevated(exe, args)
	}
	cmd := exec.CommandContext(ctx, exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: strings.TrimSpace(quote(exe) + " " + args)}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return int64(uint32(exitErr.ExitCode())), nil
	case errors.Is(err, windows.ERROR_ELEVATION_REQUIRED):
		return runElevated(exe, args)
	}
	return -1, err
}

type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      uintptr
	dwHotKey       uint32
	hIconOrMonitor uintptr
	hProcess       windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	swShowNormal          = 1
)

var (
	shell32            = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteEx = shell32.NewProc("ShellExecuteExW")
	user32             = windows.NewLazySystemDLL("user32.dll")
	procSendMsgTimeout = user32.NewProc("SendMessageTimeoutW")
)

func runElevated(exe, args string) (int64, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return -1, err
	}
	params, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return -1, err
	}
	info := shellExecuteInfo{
		fMask: seeMaskNoCloseProcess | seeMaskNoAsync, lpVerb: verb,
		lpFile: file, lpParameters: params, nShow: swShowNormal,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, e := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errors.Is(e, windows.ERROR_CANCELLED) {
			return -1, errors.New("elevation was cancelled by the user")
		}
		return -1, fmt.Errorf("elevate %s: %w", exe, e)
	}
	if info.hProcess == 0 {
		return 0, nil
	}
	defer windows.CloseHandle(info.hProcess)
	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return -1, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return -1, err
	}
	return int64(code), nil
}

const uninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

// ReadARP enumerates Apps & Features entries from HKLM (64- and 32-bit
// views) and HKCU, skipping system components and update entries.
func ReadARP() ([]ARPEntry, error) {
	type loc struct {
		root  registry.Key
		name  string
		view  uint32
		scope string
	}
	locs := []loc{
		// Key prefixes follow native winget's ARP\<scope>\<arch>\ Ids.
		{registry.LOCAL_MACHINE, `Machine\X64`, registry.WOW64_64KEY, "machine"},
		{registry.LOCAL_MACHINE, `Machine\X86`, registry.WOW64_32KEY, "machine"},
		{registry.CURRENT_USER, `User\X64`, 0, "user"},
	}
	seen := map[string]bool{}
	var out []ARPEntry
	for _, l := range locs {
		k, err := registry.OpenKey(l.root, uninstallKey, registry.READ|l.view)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		for _, n := range names {
			sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE|l.view)
			if err != nil {
				continue
			}
			str := func(v string) string { s, _, _ := sk.GetStringValue(v); return s }
			num := func(v string) uint64 { d, _, _ := sk.GetIntegerValue(v); return d }
			e := ARPEntry{
				Key: l.name + `\` + n, KeyName: n,
				DisplayName: str("DisplayName"), DisplayVersion: str("DisplayVersion"),
				Publisher: str("Publisher"), UninstallString: str("UninstallString"),
				QuietUninstallString: str("QuietUninstallString"), InstallLocation: str("InstallLocation"),
				WindowsInstaller: num("WindowsInstaller") == 1, Scope: l.scope,
			}
			skip := e.DisplayName == "" || num("SystemComponent") == 1 || str("ParentKeyName") != "" || str("ReleaseType") == "Update"
			sk.Close()
			dedupe := strings.ToLower(n + "|" + e.DisplayName + "|" + e.DisplayVersion)
			if skip || seen[dedupe] {
				continue
			}
			seen[dedupe] = true
			out = append(out, e)
		}
		k.Close()
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName) })
	return out, nil
}

// AddToUserPath appends dir to the user's PATH (HKCU\Environment) if it
// is not already present and notifies running programs. It returns true
// when PATH was changed.
func AddToUserPath(dir string) (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	cur, typ, err := k.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return false, err
	}
	for _, p := range filepath.SplitList(cur) {
		if strings.EqualFold(strings.TrimRight(p, `\`), strings.TrimRight(dir, `\`)) {
			return false, nil
		}
	}
	next := dir
	if cur != "" {
		next = strings.TrimRight(cur, ";") + ";" + dir
	}
	if typ == registry.SZ {
		err = k.SetStringValue("Path", next)
	} else {
		err = k.SetExpandStringValue("Path", next)
	}
	if err != nil {
		return false, err
	}
	env, _ := windows.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	var res uintptr
	procSendMsgTimeout.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&res)))
	return true, nil
}

// FindNativeWinget locates winget.exe: on PATH, via the App Execution
// Alias, or inside the App Installer package (needed when running as
// SYSTEM, where the alias is not available). It never returns the
// current executable.
func FindNativeWinget() string {
	self, _ := os.Executable()
	var cands []string
	if p, err := exec.LookPath("winget.exe"); err == nil {
		cands = append(cands, p)
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		cands = append(cands, filepath.Join(la, "Microsoft", "WindowsApps", "winget.exe"))
	}
	pf := os.Getenv("ProgramW6432")
	if pf == "" {
		pf = os.Getenv("ProgramFiles")
	}
	if pf != "" {
		matches, _ := filepath.Glob(filepath.Join(pf, "WindowsApps", "Microsoft.DesktopAppInstaller_*__8wekyb3d8bbwe", "winget.exe"))
		sort.Sort(sort.Reverse(sort.StringSlice(matches))) // newest version first
		cands = append(cands, matches...)
	}
	for _, c := range cands {
		if self != "" && strings.EqualFold(filepath.Clean(c), filepath.Clean(self)) {
			continue
		}
		// App execution aliases are zero-byte reparse points; Lstat
		// succeeds for them even though Stat may not.
		if _, err := os.Lstat(c); err == nil {
			return c
		}
	}
	return ""
}

// powershell runs a Windows PowerShell 5.1 script (the Appx module is
// native there) and returns stdout. The script is passed with
// -EncodedCommand, so no quoting is involved.
func powershell(ctx context.Context, script string) ([]byte, error) {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(exe); err != nil {
		exe = "powershell.exe"
	}
	u := utf16.Encode([]rune(script))
	buf := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(buf[2*i:], c)
	}
	cmd := exec.CommandContext(ctx, exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(buf))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := DecodeCLIXML(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), errors.New(msg)
	}
	return stdout.Bytes(), nil
}

const appxListScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false
$p = @(Get-AppxPackage | Where-Object { -not $_.IsResourcePackage -and -not $_.IsBundle } | ForEach-Object {
  [pscustomobject]@{
    Name = $_.Name; FullName = $_.PackageFullName; FamilyName = $_.PackageFamilyName
    Version = [string]$_.Version; Architecture = [string]$_.Architecture; Publisher = $_.Publisher
    InstallLocation = $_.InstallLocation; IsFramework = [bool]$_.IsFramework
    NonRemovable = [bool]$_.NonRemovable; SignatureKind = [string]$_.SignatureKind
  }
})
ConvertTo-Json -InputObject $p -Compress -Depth 2`

// ReadMSIX lists the MSIX/AppX packages registered for the current user
// (main and framework packages; resource packages and bundles are left
// out, as in winget) with display names resolved from their manifests.
func ReadMSIX(ctx context.Context) ([]MSIXPackage, error) {
	out, err := powershell(ctx, appxListScript)
	if err != nil {
		return nil, fmt.Errorf("Get-AppxPackage: %w", err)
	}
	pkgs, err := ParseAppxJSON(out)
	if err != nil {
		return nil, fmt.Errorf("Get-AppxPackage output: %w", err)
	}
	for i := range pkgs {
		resolveManifestNames(&pkgs[i])
	}
	sort.Slice(pkgs, func(i, j int) bool {
		return strings.ToLower(pkgs[i].DisplayName) < strings.ToLower(pkgs[j].DisplayName)
	})
	return pkgs, nil
}

var (
	shlwapi                  = windows.NewLazySystemDLL("shlwapi.dll")
	procSHLoadIndirectString = shlwapi.NewProc("SHLoadIndirectString")
)

// resolveManifestNames reads DisplayName/PublisherDisplayName from the
// package's AppxManifest.xml and resolves ms-resource references.
func resolveManifestNames(p *MSIXPackage) {
	if p.InstallLocation == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(p.InstallLocation, "AppxManifest.xml"))
	if err != nil {
		return
	}
	var m struct {
		Properties struct {
			DisplayName          string
			PublisherDisplayName string
		}
	}
	if xml.Unmarshal(b, &m) != nil {
		return
	}
	if v := resolveManifestString(p, strings.TrimSpace(m.Properties.DisplayName)); v != "" {
		p.DisplayName = v
	}
	if v := resolveManifestString(p, strings.TrimSpace(m.Properties.PublisherDisplayName)); v != "" {
		p.PublisherDisplayName = v
	}
}

func resolveManifestString(p *MSIXPackage, v string) string {
	uri := MSResourceURI(p.Name, v)
	if uri == "" {
		return v // a literal
	}
	src, err := windows.UTF16PtrFromString("@{" + p.FullName + "?" + uri + "}")
	if err != nil {
		return ""
	}
	buf := make([]uint16, 1024)
	if hr, _, _ := procSHLoadIndirectString.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); hr != 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// RemoveMSIX uninstalls an MSIX package for the current user. Failures
// are returned as *MSIXError. A package that is already gone (e.g. a
// framework Windows removed together with its last dependent) counts as
// success.
func RemoveMSIX(ctx context.Context, fullName string) error {
	// Catch the exception in the script and print "0x<HRESULT>" plus the
	// message, so we never have to read PowerShell's CLIXML error stream.
	script := `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false
try { Remove-AppxPackage -Package '` + strings.ReplaceAll(fullName, "'", "''") + `' }
catch { [Console]::Out.WriteLine(('0x{0:X8}' -f $_.Exception.HResult)); [Console]::Out.Write($_.Exception.Message); exit 1 }`
	out, err := powershell(ctx, script)
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	var hr uint32
	if first, rest, ok := strings.Cut(text, "\n"); ok || strings.HasPrefix(text, "0x") {
		if n, perr := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(first), "0x"), 16, 32); perr == nil {
			hr, text = uint32(n), rest
		}
	}
	if strings.TrimSpace(text) == "" {
		text = err.Error() // PowerShell itself failed (e.g. not available)
	}
	me := ParseMSIXError(text, hr)
	if me.HResult == HResultPackageNotFound {
		return nil
	}
	return me
}

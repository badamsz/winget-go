package installer

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"winget-go/internal/manifest"
)

func load7zip(t *testing.T) *manifest.Manifest {
	b, err := os.ReadFile("../manifest/testdata/7zip.7zip.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSelectArchitecture(t *testing.T) {
	m := load7zip(t)
	in, err := Select(m, SelectOptions{Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if in.Architecture != "arm64" {
		t.Fatalf("arch = %s", in.Architecture)
	}
	in, err = Select(m, SelectOptions{Architecture: "x64", InstallerType: "wix"})
	if err != nil {
		t.Fatal(err)
	}
	if in.InstallerType != "wix" || in.Architecture != "x64" {
		t.Fatalf("got %s/%s", in.Architecture, in.InstallerType)
	}
	if _, err := Select(m, SelectOptions{Architecture: "x64", Scope: "user"}); err == nil {
		t.Fatal("7-Zip only has machine-scope installers; expected error")
	}
}

func TestBuildArgs(t *testing.T) {
	m := load7zip(t)
	exe, _ := Select(m, SelectOptions{Architecture: "x64", InstallerType: "exe"})
	if got := BuildArgs(exe, ArgOptions{Mode: ModeSilent, InstallLocation: `C:\7z`}); got != `/S /D="C:\7z"` {
		t.Fatalf("exe args = %q", got)
	}
	msi, _ := Select(m, SelectOptions{Architecture: "x64", InstallerType: "wix"})
	got := BuildArgs(msi, ArgOptions{Mode: ModeSilentWithProgress, LogPath: `C:\l.log`, Scope: "machine", Custom: "FOO=1"})
	want := `/passive /norestart /log "C:\l.log" ALLUSERS=1 FOO=1`
	if got != want {
		t.Fatalf("msi args = %q want %q", got, want)
	}
	if got := BuildArgs(msi, ArgOptions{Override: "/x"}); got != "/x" {
		t.Fatalf("override = %q", got)
	}
	inno := &manifest.Installer{InstallerCommon: manifest.InstallerCommon{InstallerType: "inno"}}
	if got := BuildArgs(inno, ArgOptions{Mode: ModeSilent, Scope: "user"}); got != "/SP- /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /CURRENTUSER" {
		t.Fatalf("inno = %q", got)
	}
}

func TestIsSuccess(t *testing.T) {
	in := &manifest.Installer{InstallerCommon: manifest.InstallerCommon{InstallerSuccessCodes: []int64{42}}}
	for code, want := range map[int64]bool{0: true, 3010: true, 42: true, 1: false} {
		if ok, _ := IsSuccess(in, code); ok != want {
			t.Errorf("IsSuccess(%d) = %v", code, ok)
		}
	}
}

func TestExtension(t *testing.T) {
	cases := map[string]string{
		"https://x/y/setup.MSI":     ".msi",
		"https://x/y/download?id=3": ".exe",
		"https://x/y/tool.zip#frag": ".zip",
		"https://x/y.com/latest":    ".exe",
	}
	for u, want := range cases {
		in := &manifest.Installer{InstallerUrl: u}
		if got := Extension(in); got != want {
			t.Errorf("Extension(%s) = %s want %s", u, got, want)
		}
	}
}

func TestSplitCommand(t *testing.T) {
	cases := []struct{ in, exe, args string }{
		{`"C:\Program Files\7-Zip\Uninstall.exe"`, `C:\Program Files\7-Zip\Uninstall.exe`, ``},
		{`"C:\Program Files\App\unins000.exe" /SILENT`, `C:\Program Files\App\unins000.exe`, `/SILENT`},
		{`C:\Program Files\App\uninst.exe /S`, `C:\Program Files\App\uninst.exe`, `/S`},
		{`MsiExec.exe /X{1234}`, `MsiExec.exe`, `/X{1234}`},
		{`rundll32 foo.dll,Bar`, `rundll32`, `foo.dll,Bar`},
	}
	for _, c := range cases {
		exe, args := SplitCommand(c.in)
		if exe != c.exe || args != c.args {
			t.Errorf("SplitCommand(%q) = %q, %q", c.in, exe, args)
		}
	}
}

func TestUnzipRejectsTraversal(t *testing.T) {
	if _, err := safeJoin(t.TempDir(), "../evil.exe"); err == nil {
		t.Fatal("expected traversal error")
	}
	if _, err := safeJoin(t.TempDir(), `bin\tool.exe`); err != nil {
		t.Fatal(err)
	}
}

// pwsh77 mirrors Microsoft.PowerShell 7.7.0.0: one msixbundle listed for
// three architectures, all marked Windows.Universal.
const pwsh77 = `PackageIdentifier: Microsoft.PowerShell
PackageVersion: 7.7.0.0
PackageName: PowerShell
Publisher: Microsoft Corporation
ManifestType: merged
InstallerType: msix
Platform:
- Windows.Universal
Installers:
- Architecture: x64
  InstallerUrl: https://example.invalid/PowerShell-7.7.0.msixbundle
  InstallerSha256: E00C349858C2D273F29485541ECE91617B435ACCDD5E3F156D68D5CB97740022
- Architecture: x86
  InstallerUrl: https://example.invalid/PowerShell-7.7.0.msixbundle
  InstallerSha256: E00C349858C2D273F29485541ECE91617B435ACCDD5E3F156D68D5CB97740022
- Architecture: arm64
  InstallerUrl: https://example.invalid/PowerShell-7.7.0.msixbundle
  InstallerSha256: E00C349858C2D273F29485541ECE91617B435ACCDD5E3F156D68D5CB97740022
`

func TestSelectUniversalPlatform(t *testing.T) {
	m, err := manifest.Parse([]byte(pwsh77))
	if err != nil {
		t.Fatal(err)
	}
	in, err := Select(m, SelectOptions{Architecture: "x64"})
	if err != nil {
		t.Fatalf("Windows.Universal installers must be applicable: %v", err)
	}
	if in.Architecture != "x64" {
		t.Fatalf("arch = %s", in.Architecture)
	}
}

func TestSelectInstallTechnology(t *testing.T) {
	m, _ := manifest.Parse([]byte(pwsh77))
	// Upgrading an MSI install with an MSIX-only release.
	_, err := Select(m, SelectOptions{Architecture: "x64", InstalledType: "msi"})
	var tm *TechMismatchError
	if !errors.As(err, &tm) || tm.Installed != "msi" || !slices.Equal(tm.Available, []string{"msix"}) {
		t.Fatalf("expected tech mismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "--installer-type msix") {
		t.Fatalf("message should suggest --installer-type: %v", err)
	}
	// An explicit --installer-type overrides the restriction.
	if _, err := Select(m, SelectOptions{Architecture: "x64", InstalledType: "msi", InstallerType: "msix"}); err != nil {
		t.Fatal(err)
	}
	// MSIX upgrading MSIX is fine.
	if _, err := Select(m, SelectOptions{Architecture: "x64", InstalledType: "msix"}); err != nil {
		t.Fatal(err)
	}

	// 7-Zip ships both exe and wix installers: the installed technology
	// decides which one an upgrade uses.
	z := load7zip(t)
	for installed, want := range map[string]string{"msi": "msi", "wix": "msi", "exe": "exe", "nullsoft": "exe"} {
		in, err := Select(z, SelectOptions{Architecture: "x64", InstalledType: installed})
		if err != nil || TechFamily(in.InstallerType) != want {
			t.Errorf("installed %s: got %v %v", installed, in, err)
		}
	}
}

func TestSelectRejectionReasons(t *testing.T) {
	m, _ := manifest.Parse([]byte(pwsh77))
	_, err := Select(m, SelectOptions{Architecture: "x64", Scope: "machine", InstallerType: "msi"})
	if err == nil || !strings.Contains(err.Error(), "x64/msix (installer type)") || !strings.Contains(err.Error(), "arm64/msix (architecture)") {
		t.Fatalf("expected per-installer reasons, got %v", err)
	}
}

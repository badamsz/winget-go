package engine

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"winget-go/internal/installed"
	"winget-go/internal/installer"
	"winget-go/internal/manifest"
)

func TestMatchInstalledWildcard(t *testing.T) {
	items := []*installed.Item{
		pkg("Microsoft.DotNet.Runtime.8", arp("Microsoft .NET Runtime - 8.0.11 (x64)", "8.0.11", "Microsoft Corporation", "machine")),
		pkg("Microsoft.DotNet.SDK.8", arp("Microsoft .NET SDK 8.0.404 (x64)", "8.0.404", "Microsoft Corporation", "machine")),
		arp("Python 3.11.9 (64-bit)", "3.11.9", "PSF", "machine"),
		arp("Python 3.12.1 (64-bit)", "3.12.1", "PSF", "machine"),
		arp("Python Launcher", "3.12.1", "PSF", "machine"),
		msixItem("WindowsAppRuntime.1.7", "Microsoft.WindowsAppRuntime.1.7_8wekyb3d8bbwe", "7000.1.0.0", "x64", "Microsoft.WindowsAppRuntime.1.7"),
		msixItem("Snipping Tool", "Microsoft.ScreenSketch_8wekyb3d8bbwe", "11.2609.15.0", "x64", ""),
	}
	cases := map[string][]string{
		"microsoft.dotnet.*":            {"8.0.11", "8.0.404"},
		"microsoft.dotnet.sdk.?":        {"8.0.404"},
		"python 3.1[12].* (*)":          {"3.11.9", "3.12.1"},
		"python *":                      {"3.11.9", "3.12.1", "3.12.1"},
		"python":                        nil, // glob must match the whole name
		"*launcher":                     {"3.12.1"},
		"microsoft.windowsappruntime.*": {"7000.1.0.0"},
		"windowsappruntime.*":           {"7000.1.0.0"}, // display name
		`msix\microsoft.screensketch_*`: {"11.2609.15.0"},
		`arp\hklm\python 3.12*`:         {"3.12.1"}, // backslashes are literal
	}
	for pattern, want := range cases {
		got := versions(matchInstalled(items, globPattern(pattern), false, true))
		if !slices.Equal(got, want) {
			t.Errorf("%q matched %v, want %v", pattern, got, want)
		}
	}
	// Without wildcards the old substring behaviour applies.
	if got := matchInstalled(items, "python", false, false); len(got) != 3 {
		t.Errorf("substring match = %d items", len(got))
	}
}

func TestInstalledTech(t *testing.T) {
	msi := arp("PowerShell 7-x64", "7.6.10.0", "Microsoft Corporation", "machine")
	msi.ARP.WindowsInstaller = true
	exe := arp("Tool", "1.0", "X", "machine")
	rec := arp("Thing", "1.0", "X", "machine")
	rec.Record = &installed.Record{InstallerType: "nullsoft"}
	m := msixItem("PowerShell", "Microsoft.PowerShell_8wekyb3d8bbwe", "7.6.10.0", "x64", "")
	for it, want := range map[*installed.Item]string{msi: "msi", exe: "", rec: "nullsoft", m: "msix", nil: ""} {
		if got := InstalledTech(it); got != want {
			t.Errorf("InstalledTech(%v) = %q want %q", it, got, want)
		}
	}
}

func TestSelectForUpgradeMigrate(t *testing.T) {
	const pwsh = `PackageIdentifier: Microsoft.PowerShell
PackageVersion: 7.7.0.0
PackageName: PowerShell
Publisher: Microsoft Corporation
ManifestType: merged
InstallerType: msix
Platform: [Windows.Universal]
Installers:
- Architecture: x64
  InstallerUrl: https://example.invalid/PowerShell-7.7.0.msixbundle
  InstallerSha256: E00C349858C2D273F29485541ECE91617B435ACCDD5E3F156D68D5CB97740022
`
	m, err := manifest.Parse([]byte(pwsh))
	if err != nil {
		t.Fatal(err)
	}
	sel := installer.SelectOptions{Architecture: "x64", InstalledType: "msi"}
	// Without --migrate: the technology mismatch is reported.
	var tm *installer.TechMismatchError
	if _, _, err := selectForUpgrade(m, sel, false); !errors.As(err, &tm) || !strings.Contains(err.Error(), "--migrate") {
		t.Fatalf("expected TechMismatchError mentioning --migrate, got %v", err)
	}
	// With --migrate: the MSIX is chosen and the old technology reported.
	in, from, err := selectForUpgrade(m, sel, true)
	if err != nil || in.InstallerType != "msix" || from != "msi" {
		t.Fatalf("migrate: %v %q %v", in, from, err)
	}
	// Same technology available: no migration even with --migrate.
	sel.InstalledType = "msix"
	if _, from, err := selectForUpgrade(m, sel, true); err != nil || from != "" {
		t.Fatalf("same tech: %q %v", from, err)
	}
	// 7-Zip offers exe and msi: an MSI install stays MSI with --migrate.
	b, _ := os.ReadFile("../manifest/testdata/7zip.7zip.yaml")
	z, _ := manifest.Parse(b)
	if in, from, err := selectForUpgrade(z, installer.SelectOptions{Architecture: "x64", InstalledType: "msi"}, true); err != nil || from != "" || installer.TechFamily(in.InstallerType) != "msi" {
		t.Fatalf("7zip: %v %q %v", in, from, err)
	}
}

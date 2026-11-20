package engine

import (
	"slices"
	"testing"

	"winget-go/internal/installed"
	"winget-go/internal/platform"
	"winget-go/internal/source"
)

func arp(name, ver, publisher, scope string) *installed.Item {
	return &installed.Item{
		Name: name, Version: ver, Publisher: publisher,
		ARP: &platform.ARPEntry{Key: "HKLM\\" + name, DisplayName: name, DisplayVersion: ver, Publisher: publisher, Scope: scope},
	}
}

func pkg(id string, it *installed.Item) *installed.Item {
	it.Package = &source.Package{ID: id}
	return it
}

func versions(items []*installed.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Version)
	}
	return out
}

func TestPlanOldVersionsMajor(t *testing.T) {
	items := []*installed.Item{
		arp("Python 3.11.4 (64-bit)", "3.11.4150.0", "Python Software Foundation", "machine"),
		arp("Python 3.11.9 (64-bit)", "3.11.9150.0", "Python Software Foundation", "machine"),
		arp("Python 3.12.1 (64-bit)", "3.12.1150.0", "Python Software Foundation", "machine"),
		arp("Python 2.7.18 (64-bit)", "2.7.18150", "Python Software Foundation", "machine"),
		arp("Python 3.10.0 (32-bit)", "3.10.0", "Python Software Foundation", "machine"), // other arch
		arp("Python Launcher", "3.12.1", "Python Software Foundation", "machine"),        // other product
	}
	groups := PlanOldVersions(items, LineMajor, 1)
	if len(groups) != 1 {
		t.Fatalf("groups = %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Line != "3" || !slices.Equal(versions(g.Keep), []string{"3.12.1150.0"}) ||
		!slices.Equal(versions(g.Remove), []string{"3.11.9150.0", "3.11.4150.0"}) {
		t.Fatalf("major plan: line=%s keep=%v remove=%v", g.Line, versions(g.Keep), versions(g.Remove))
	}
}

func TestPlanOldVersionsMinorAndKeep(t *testing.T) {
	items := []*installed.Item{
		arp("Python 3.11.4 (64-bit)", "3.11.4", "PSF", "machine"),
		arp("Python 3.11.7 (64-bit)", "3.11.7", "PSF", "machine"),
		arp("Python 3.11.9 (64-bit)", "3.11.9", "PSF", "machine"),
		arp("Python 3.12.1 (64-bit)", "3.12.1", "PSF", "machine"),
	}
	groups := PlanOldVersions(items, LineMinor, 1)
	if len(groups) != 1 || groups[0].Line != "3.11" || !slices.Equal(versions(groups[0].Remove), []string{"3.11.7", "3.11.4"}) {
		t.Fatalf("minor plan: %+v", groups)
	}
	groups = PlanOldVersions(items, LineMinor, 2)
	if len(groups) != 1 || !slices.Equal(versions(groups[0].Remove), []string{"3.11.4"}) {
		t.Fatalf("keep=2 plan: %+v", groups)
	}
	if groups := PlanOldVersions(items, LineAll, 1); len(groups) != 1 || len(groups[0].Remove) != 3 {
		t.Fatalf("all plan: %+v", groups)
	}
}

func TestPlanOldVersionsSafety(t *testing.T) {
	items := []*installed.Item{
		// Same version twice (e.g. two install locations): never removed.
		arp("Tool", "1.2.0", "Contoso", "machine"),
		arp("Tool", "1.2", "Contoso", "machine"),
		// Per-user vs per-machine are separate groups.
		arp("Editor", "5.1", "Fabrikam", "user"),
		arp("Editor", "5.0", "Fabrikam", "machine"),
		// Unknown versions are skipped, not removed.
		arp("Thing", "", "X", "machine"),
		arp("Thing", "1.0", "X", "machine"),
		// Same name but different publisher: different products.
		arp("Agent", "2.0", "A Corp", "machine"),
		arp("Agent", "1.0", "B Corp", "machine"),
	}
	if groups := PlanOldVersions(items, LineAll, 1); len(groups) != 0 {
		t.Fatalf("expected nothing to remove, got %+v", groups)
	}
}

func TestPlanOldVersionsMatchedPackage(t *testing.T) {
	// Entries matched to the same package group by Id even if the
	// display names differ.
	items := []*installed.Item{
		pkg("Microsoft.DotNet.Runtime.8", arp("Microsoft .NET Runtime - 8.0.1 (x64)", "8.0.1", "Microsoft Corporation", "machine")),
		pkg("Microsoft.DotNet.Runtime.8", arp("Microsoft .NET Runtime - 8.0.11 (x64)", "8.0.11", "Microsoft Corporation", "machine")),
		pkg("Microsoft.DotNet.Runtime.8", arp("Microsoft .NET Runtime - 8.0.4 (x86)", "8.0.4", "Microsoft Corporation", "machine")),
	}
	groups := PlanOldVersions(items, LineMajor, 1)
	if len(groups) != 1 || !slices.Equal(versions(groups[0].Remove), []string{"8.0.1"}) {
		t.Fatalf("plan: %+v", groups)
	}
}

func TestParseVersionLine(t *testing.T) {
	for in, want := range map[string]VersionLine{"": LineMajor, "MAJOR": LineMajor, "minor": LineMinor, "all": LineAll} {
		if got, err := ParseVersionLine(in); err != nil || got != want {
			t.Errorf("ParseVersionLine(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseVersionLine("patch"); err == nil {
		t.Error("expected error")
	}
}

func msixItem(name, family, ver, arch string, id string) *installed.Item {
	it := &installed.Item{Name: name, Version: ver, MSIX: &platform.MSIXPackage{
		Name: family[:len(family)-len("_8wekyb3d8bbwe")], FamilyName: family, Version: ver, Architecture: arch,
		FullName: family[:len(family)-len("_8wekyb3d8bbwe")] + "_" + ver + "_" + arch + "__8wekyb3d8bbwe",
	}}
	if id != "" {
		it.Package = &source.Package{ID: id}
	}
	return it
}

func TestPlanOldVersionsMSIX(t *testing.T) {
	const rt = "Microsoft.WindowsAppRuntime.1.8"
	items := []*installed.Item{
		msixItem("WindowsAppRuntime.1.8", rt+"_8wekyb3d8bbwe", "8000.675.1142.0", "x64", rt),
		msixItem("WindowsAppRuntime.1.8", rt+"_8wekyb3d8bbwe", "8000.616.304.0", "x64", rt),
		msixItem("WindowsAppRuntime.1.8", rt+"_8wekyb3d8bbwe", "8000.616.304.0", "x86", rt), // other arch: kept
		// A different family mapped to the same winget Id must not be
		// compared with the framework above.
		msixItem("Windows App Runtime DDLM", "Microsoft.WinAppRuntime.DDLM.8000.675.1142.0-x6_8wekyb3d8bbwe", "8000.675.1142.0", "x64", rt),
		msixItem("WindowsAppRuntime.1.8 Main", "MicrosoftCorporationII.WinAppRuntime.Main.1.8_8wekyb3d8bbwe", "1.0.0.0", "x64", rt),
	}
	groups := PlanOldVersions(items, LineAll, 1)
	if len(groups) != 1 || !slices.Equal(versions(groups[0].Remove), []string{"8000.616.304.0"}) ||
		groups[0].Remove[0].MSIX.Architecture != "x64" {
		t.Fatalf("plan: %+v", groups)
	}
}

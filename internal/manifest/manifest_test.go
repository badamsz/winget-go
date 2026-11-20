package manifest

import (
	"os"
	"testing"
)

func load(t *testing.T) *Manifest {
	t.Helper()
	b, err := os.ReadFile("testdata/7zip.7zip.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseMerged(t *testing.T) {
	m := load(t)
	if m.PackageIdentifier != "7zip.7zip" || m.PackageVersion != "26.06" || m.Publisher != "Igor Pavlov" {
		t.Fatalf("bad header: %+v", m)
	}
	if len(m.Installers) != 6 {
		t.Fatalf("installers = %d", len(m.Installers))
	}
	// Root-level Scope/UpgradeBehavior/ElevationRequirement must be inherited.
	for _, in := range m.Installers {
		if in.Scope != "machine" || in.UpgradeBehavior != "install" || in.ElevationRequirement != "elevatesSelf" {
			t.Fatalf("defaults not merged into %s/%s: %+v", in.Architecture, in.InstallerType, in.InstallerCommon)
		}
	}
	if got := m.Installers[1].InstallerSwitches.Silent; got != "/S" {
		t.Fatalf("silent switch = %q", got)
	}
	if len(m.Localizations) != 1 || m.Localizations[0].PackageLocale != "zh-CN" {
		t.Fatalf("localizations = %+v", m.Localizations)
	}
}

func TestLocalized(t *testing.T) {
	m := load(t)
	zh := m.Localized("zh-CN")
	if zh.PackageName != "7-Zip" { // inherited from default locale
		t.Fatalf("name = %q", zh.PackageName)
	}
	if zh.PackageUrl != "https://sparanoid.com/lab/7z/download.html" {
		t.Fatalf("url = %q", zh.PackageUrl)
	}
	if en := m.Localized(""); en.ShortDescription != "A file archiver with a high compression ratio." {
		t.Fatalf("desc = %q", en.ShortDescription)
	}
}

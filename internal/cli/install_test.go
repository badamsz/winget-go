package cli

import (
	"testing"

	"winget-go/internal/engine"
	"winget-go/internal/installed"
	"winget-go/internal/source"
)

func up(id, name, excludedBy string) engine.Upgrade {
	return engine.Upgrade{Item: &installed.Item{Name: name, Package: &source.Package{ID: id}}, Available: "2.0", ExcludedBy: excludedBy}
}

func TestSplitAndDedupeUpgrades(t *testing.T) {
	ups := []engine.Upgrade{
		up("Microsoft.VCRedist.2015+.x64", "Microsoft Visual C++ v14 Redistributable (x64)", ""),
		up("Microsoft.VCRedist.2015+.x64", "Microsoft Visual C++ 2022 X64 Minimum Runtime", ""), // same package, second ARP entry
		up("Microsoft.VCRedist.2015+.x86", "Microsoft Visual C++ v14 Redistributable (x86)", ""),
		up("Microsoft.VCRedist.2013.x64", "Microsoft Visual C++ 2013 Redistributable (x64)", "Microsoft.VCRedist.2013.*"),
	}
	allowed, excluded := splitExcluded(ups)
	if len(allowed) != 3 || len(excluded) != 1 {
		t.Fatalf("split: %d allowed, %d excluded", len(allowed), len(excluded))
	}
	if d := dedupeUpgrades(allowed); len(d) != 2 || d[0].Item.Package.ID != "Microsoft.VCRedist.2015+.x64" || d[1].Item.Package.ID != "Microsoft.VCRedist.2015+.x86" {
		t.Fatalf("dedupe: %+v", d)
	}
}

func TestMatchUpgradesWithWildcard(t *testing.T) {
	ups := []engine.Upgrade{
		up("Microsoft.VCRedist.2015+.x64", "Microsoft Visual C++ v14 Redistributable (x64)", ""),
		up("Microsoft.VCRedist.2015+.x86", "Microsoft Visual C++ v14 Redistributable (x86)", ""),
		up("Microsoft.PowerShell", "PowerShell 7-x64", ""),
	}
	var items []*installed.Item
	for _, u := range ups {
		items = append(items, u.Item)
	}
	for term, want := range map[string]int{
		"Microsoft.VCRedist.*":         2,
		"microsoft.vcredist.2015+.x6?": 1,
		"*Redistributable (x86)":       1,
		"Microsoft.*":                  3,
		"Microsoft.PowerShell":         1, // exact Id
		"vcredist":                     2, // substring, as before
	} {
		got, err := engine.MatchInstalled(items, term, false)
		if err != nil || len(got) != want {
			t.Errorf("%q matched %d (%v), want %d", term, len(got), err, want)
		}
	}
	if _, err := engine.MatchInstalled(items, "Microsoft.[", false); err == nil {
		t.Error("expected invalid pattern error")
	}
}

package installed

import (
	"context"
	"fmt"
	"io"
	"strings"

	"winget-go/internal/platform"
	"winget-go/internal/source"
)

// Item is an installed application, possibly matched to a package.
type Item struct {
	Name      string
	Version   string
	Publisher string
	ARP       *platform.ARPEntry    // Apps & Features entry, if any
	MSIX      *platform.MSIXPackage // MSIX/AppX package, if any
	Record    *Record               // set when installed by winget-go
	Package   *source.Package       // matched package (nil if unknown)
}

// ID returns the package id or the ARP key name for unmatched entries.
func (it *Item) ID() string {
	switch {
	case it.Package != nil:
		return it.Package.ID
	case it.Record != nil:
		return it.Record.ID
	case it.ARP != nil:
		return "ARP\\" + it.ARP.Key
	case it.MSIX != nil:
		return "MSIX\\" + it.MSIX.FullName
	}
	return ""
}

// SourceName returns the matched source name, or "".
func (it *Item) SourceName() string {
	if it.Package != nil {
		return it.Package.Source.Config.Name
	}
	return ""
}

// List enumerates installed applications (ARP entries and MSIX
// packages) and correlates them with the given sources: first via
// winget-go's own install records (exact), then ARP product codes or MSIX
// package family names, then normalized name/publisher. MSIX enumeration
// failures are reported on warn (if non-nil) and otherwise ignored.
func List(ctx context.Context, st *State, sources []*source.Source, warn io.Writer) ([]*Item, error) {
	arp, err := platform.ReadARP()
	if err != nil {
		return nil, err
	}
	msix, err := platform.ReadMSIX(ctx)
	if err != nil && warn != nil {
		fmt.Fprintf(warn, "warning: MSIX packages are not listed: %v\n", err)
	}
	byKey := map[string]*Record{}
	for _, r := range st.Packages {
		for _, k := range r.ARPKeys {
			byKey[strings.ToLower(platform.LegacyARPKey(k))] = r
		}
	}
	lookup := func(id, srcName string) *source.Package {
		for _, s := range sources {
			if srcName != "" && !strings.EqualFold(s.Config.Name, srcName) {
				continue
			}
			if p, _ := s.Lookup(id); p != nil {
				return p
			}
		}
		return nil
	}
	var items []*Item
	used := map[*Record]bool{}
	for i := range arp {
		e := &arp[i]
		it := &Item{Name: e.DisplayName, Version: e.DisplayVersion, Publisher: e.Publisher, ARP: e}
		if r := byKey[strings.ToLower(e.Key)]; r != nil {
			it.Record = r
			used[r] = true
			it.Package = lookup(r.ID, r.Source)
		}
		for _, s := range sources {
			if it.Package != nil {
				break
			}
			if ps, _ := s.ByProductCode(e.KeyName); len(ps) == 1 {
				it.Package = ps[0]
				break
			}
			if ps, _ := s.ByNormalizedName(e.DisplayName, e.Publisher); len(ps) == 1 {
				it.Package = ps[0]
			}
		}
		items = append(items, it)
	}
	for i := range msix {
		m := &msix[i]
		it := &Item{Name: m.DisplayName, Version: m.Version, Publisher: m.PublisherDisplayName, MSIX: m}
		for _, s := range sources {
			if ps, _ := s.ByFamilyName(m.FamilyName); len(ps) == 1 {
				it.Package = ps[0]
				break
			}
		}
		// MSIX installs leave no ARP entry, so their record (if winget-go
		// installed them) is linked by package Id instead.
		if it.Package != nil {
			if r := st.Get(it.Package.ID); r != nil && len(r.ARPKeys) == 0 && !used[r] {
				it.Record = r
				used[r] = true
			}
		}
		items = append(items, it)
	}
	// Portable packages (and anything whose ARP entry disappeared) come
	// only from our own records.
	for _, r := range st.Sorted() {
		if used[r] {
			continue
		}
		if len(r.ARPKeys) > 0 && platform.IsWindows() {
			continue // ARP entry gone: uninstalled outside winget-go
		}
		items = append(items, &Item{Name: r.Name, Version: r.Version, Record: r, Package: lookup(r.ID, r.Source)})
	}
	return items, nil
}

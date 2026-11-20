package installed

import (
	"strings"

	"winget-go/internal/platform"
	"winget-go/internal/source"
)

// Item is an installed application, possibly matched to a package.
type Item struct {
	Name      string
	Version   string
	Publisher string
	ARP       *platform.ARPEntry // nil for portable packages
	Record    *Record            // set when installed by winget-go
	Package   *source.Package    // matched package (nil if unknown)
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

// List enumerates installed applications and correlates them with the
// given sources: first via winget-go's own install records (exact), then
// ARP product codes, then normalized name/publisher.
func List(st *State, sources []*source.Source) ([]*Item, error) {
	arp, err := platform.ReadARP()
	if err != nil {
		return nil, err
	}
	byKey := map[string]*Record{}
	for _, r := range st.Packages {
		for _, k := range r.ARPKeys {
			byKey[strings.ToLower(k)] = r
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

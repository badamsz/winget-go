// Package installed tracks packages installed by winget-go and correlates
// Apps & Features (ARP) entries with source packages.
package installed

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"winget-go/internal/config"
	"winget-go/internal/platform"
)

// Record describes a package installed by winget-go.
type Record struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Version         string    `json:"version"`
	Source          string    `json:"source"`
	InstallerType   string    `json:"installerType"`
	Scope           string    `json:"scope,omitempty"`
	Architecture    string    `json:"architecture,omitempty"`
	InstallLocation string    `json:"installLocation,omitempty"`
	Links           []string  `json:"links,omitempty"`   // portable command links/shims
	ARPKeys         []string  `json:"arpKeys,omitempty"` // ARP entries created by the installer
	FromGitHub      bool      `json:"fromGitHub,omitempty"`
	InstalledAt     time.Time `json:"installedAt"`
}

// State is the persisted set of records.
type State struct {
	Packages map[string]*Record `json:"packages"`
}

func statePath() string { return filepath.Join(config.DataDir(), "installed.json") }

// Load reads the state file.
func Load() (*State, error) {
	s := &State{Packages: map[string]*Record{}}
	b, err := os.ReadFile(statePath())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.Packages == nil {
		s.Packages = map[string]*Record{}
	}
	return s, nil
}

// Save writes the state file atomically.
func (s *State) Save() error {
	if err := os.MkdirAll(config.DataDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := statePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, statePath())
}

// Get returns a record by package id (case-insensitive).
func (s *State) Get(id string) *Record { return s.Packages[strings.ToLower(id)] }

// Put stores a record.
func (s *State) Put(r *Record) { s.Packages[strings.ToLower(r.ID)] = r }

// Remove deletes a record.
func (s *State) Remove(id string) { delete(s.Packages, strings.ToLower(id)) }

// Sorted returns records ordered by id.
func (s *State) Sorted() []*Record {
	out := make([]*Record, 0, len(s.Packages))
	for _, r := range s.Packages {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
	return out
}

// Snapshot captures ARP entries keyed by Key.
func Snapshot() map[string]platform.ARPEntry {
	entries, _ := platform.ReadARP()
	m := make(map[string]platform.ARPEntry, len(entries))
	for _, e := range entries {
		m[e.Key] = e
	}
	return m
}

// Diff returns entries that are new, or whose version changed, between
// two snapshots. This is how winget itself associates an installer with
// the ARP entry it creates.
func Diff(before, after map[string]platform.ARPEntry) []platform.ARPEntry {
	var out []platform.ARPEntry
	for k, e := range after {
		if b, ok := before[k]; !ok || b.DisplayVersion != e.DisplayVersion {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

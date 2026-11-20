package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"winget-go/internal/config"
	"winget-go/internal/github"
	"winget-go/internal/installed"
	"winget-go/internal/installer"
	"winget-go/internal/platform"
	"winget-go/internal/source"
	"winget-go/internal/version"
)

// UninstallOptions controls uninstall behaviour.
type UninstallOptions struct {
	Mode  installer.Mode
	Purge bool
}

// Uninstall removes an installed item.
func (e *Engine) Uninstall(ctx context.Context, it *installed.Item, o UninstallOptions) error {
	if !platform.IsWindows() {
		return platform.ErrUnsupported
	}
	st, err := e.State()
	if err != nil {
		return err
	}
	rec := it.Record
	if rec != nil && (rec.InstallerType == "portable" || rec.InstallerType == "zip/portable") {
		if err := installer.RemovePortable(rec.InstallLocation, rec.Links); err != nil {
			return err
		}
		st.Remove(rec.ID)
		fmt.Fprintln(e.Out, "Successfully uninstalled")
		return st.Save()
	}
	a := it.ARP
	if a == nil {
		return fmt.Errorf("%s has no uninstall information", it.Name)
	}
	exe, args, err := e.uninstallCommand(ctx, it, o)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Starting package uninstall of %s...\n", it.Name)
	elevate := a.Scope == "machine" && !platform.IsElevated()
	code, err := platform.Run(ctx, exe, args, elevate)
	if err != nil {
		return err
	}
	if code != 0 && code != 3010 && code != 1641 {
		return fmt.Errorf("uninstall failed with exit code %d (0x%08X)", code, uint32(code))
	}
	if rec != nil {
		// Several ARP entries can belong to one record (side-by-side
		// versions); forget only the one that was removed.
		var keep []string
		for _, k := range rec.ARPKeys {
			if !strings.EqualFold(k, a.Key) {
				keep = append(keep, k)
			}
		}
		rec.ARPKeys = keep
		if len(keep) == 0 {
			st.Remove(rec.ID)
		}
		if err := st.Save(); err != nil {
			return err
		}
	}
	fmt.Fprintln(e.Out, "Successfully uninstalled")
	return nil
}

func (e *Engine) uninstallCommand(ctx context.Context, it *installed.Item, o UninstallOptions) (string, string, error) {
	a := it.ARP
	isMSI := a.WindowsInstaller || (strings.HasPrefix(a.KeyName, "{") && strings.Contains(strings.ToLower(a.UninstallString), "msiexec"))
	if isMSI {
		mode := "/quiet /norestart"
		switch o.Mode {
		case installer.ModeSilentWithProgress:
			mode = "/passive /norestart"
		case installer.ModeInteractive:
			mode = ""
		}
		root := os.Getenv("SystemRoot")
		return root + `\System32\msiexec.exe`, strings.TrimSpace("/x " + a.KeyName + " " + mode), nil
	}
	if o.Mode != installer.ModeInteractive && a.QuietUninstallString != "" {
		exe, args := installer.SplitCommand(a.QuietUninstallString)
		return exe, args, nil
	}
	if a.UninstallString == "" {
		return "", "", fmt.Errorf("%s has no uninstall command", it.Name)
	}
	exe, args := installer.SplitCommand(a.UninstallString)
	if o.Mode == installer.ModeInteractive {
		return exe, args, nil
	}
	t := ""
	if it.Record != nil {
		t = it.Record.InstallerType
	} else if it.Package != nil {
		if m, _, err := it.Package.Manifest(ctx, ""); err == nil {
			if in, err := installer.Select(m, installer.SelectOptions{}); err == nil {
				t = in.InstallerType
			}
		}
	}
	if extra := installer.UninstallSilentArgs(t); extra != "" {
		args = strings.TrimSpace(args + " " + extra)
	} else {
		fmt.Fprintln(e.Err, "warning: no silent uninstall switches known for this application; the uninstaller may show UI")
	}
	return exe, args, nil
}

// Upgrade is an installed item with a newer available version.
type Upgrade struct {
	Item      *installed.Item
	Available string
	ViaGitHub bool
	// ExcludedBy is the exclusion pattern that matched, if any. Excluded
	// upgrades are still returned so callers can report them.
	ExcludedBy string
}

// Excluded reports whether the upgrade is blocked by the exclusion list.
func (u Upgrade) Excluded() bool { return u.ExcludedBy != "" }

// IsExcluded checks a package against the engine's exclusion list.
func (e *Engine) IsExcluded(id, name string) (string, bool) {
	return e.Exclusions.Match(id, name)
}

// Upgrades lists installed items with newer versions available.
func (e *Engine) Upgrades(ctx context.Context, includeUnknown bool) ([]Upgrade, error) {
	items, err := e.Installed()
	if err != nil {
		return nil, err
	}
	var out []Upgrade
	for _, it := range items {
		if it.Package == nil {
			continue
		}
		cur := it.Version
		if (cur == "" || strings.EqualFold(cur, "unknown")) && !includeUnknown {
			continue
		}
		excludedBy, _ := e.IsExcluded(it.Package.ID, it.Name)
		avail, viaGH := it.Package.LatestVersion, false
		if e.GitHub && excludedBy == "" { // don't spend API calls on excluded packages
			if m, _, err := it.Package.Manifest(ctx, ""); err == nil {
				if rel, _, err := github.ReleaseInfo(ctx, m); err == nil && rel != nil && !rel.Prerelease {
					if v := github.TagVersion(rel.TagName); version.Compare(v, avail) > 0 {
						avail, viaGH = v, true
					}
				}
			}
		}
		if version.Compare(cur, avail) < 0 {
			out = append(out, Upgrade{Item: it, Available: avail, ViaGitHub: viaGH, ExcludedBy: excludedBy})
		}
	}
	return out, nil
}

// ExportFile is winget's import/export JSON format.
type ExportFile struct {
	Schema        string         `json:"$schema"`
	CreationDate  string         `json:"CreationDate"`
	Sources       []ExportSource `json:"Sources"`
	WinGetVersion string         `json:"WinGetVersion"`
}

// ExportSource groups packages by source.
type ExportSource struct {
	Packages      []ExportPackage `json:"Packages"`
	SourceDetails struct {
		Argument   string `json:"Argument"`
		Identifier string `json:"Identifier"`
		Name       string `json:"Name"`
		Type       string `json:"Type"`
	} `json:"SourceDetails"`
}

// ExportPackage is one exported package.
type ExportPackage struct {
	PackageIdentifier string `json:"PackageIdentifier"`
	Version           string `json:"Version,omitempty"`
}

// Export builds an export document of matched installed packages.
func (e *Engine) Export(includeVersions bool) (*ExportFile, error) {
	items, err := e.Installed()
	if err != nil {
		return nil, err
	}
	f := &ExportFile{
		Schema:        "https://aka.ms/winget-packages.schema.2.0.json",
		CreationDate:  time.Now().Format(time.RFC3339),
		WinGetVersion: "winget-go-" + config.Version,
	}
	bySrc := map[*source.Source]*ExportSource{}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Package == nil || seen[strings.ToLower(it.Package.ID)] {
			continue
		}
		seen[strings.ToLower(it.Package.ID)] = true
		s := it.Package.Source
		es := bySrc[s]
		if es == nil {
			es = &ExportSource{}
			es.SourceDetails.Argument = s.Config.Arg
			es.SourceDetails.Identifier = s.Config.Identifier
			es.SourceDetails.Name = s.Config.Name
			es.SourceDetails.Type = s.Config.Type
			bySrc[s] = es
		}
		p := ExportPackage{PackageIdentifier: it.Package.ID}
		if includeVersions {
			p.Version = it.Version
		}
		es.Packages = append(es.Packages, p)
	}
	for _, s := range e.Sources {
		if es := bySrc[s]; es != nil {
			f.Sources = append(f.Sources, *es)
		}
	}
	return f, nil
}

// LoadExport reads an import file.
func LoadExport(path string) (*ExportFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f ExportFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// SourceByName returns an opened source by name.
func (e *Engine) SourceByName(name string) *source.Source {
	for _, s := range e.Sources {
		if strings.EqualFold(s.Config.Name, name) {
			return s
		}
	}
	return nil
}

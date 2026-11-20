package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"winget-go/internal/config"
	"winget-go/internal/github"
	"winget-go/internal/httpx"
	"winget-go/internal/installed"
	"winget-go/internal/installer"
	"winget-go/internal/manifest"
	"winget-go/internal/platform"
	"winget-go/internal/source"
	"winget-go/internal/version"
)

// Request describes an install, upgrade or download.
type Request struct {
	Package          *source.Package
	Version          string
	Select           installer.SelectOptions
	Mode             installer.Mode
	Location         string
	Override         string
	Custom           string
	LogPath          string
	AcceptAgreements bool
	IgnoreHash       bool
	SkipDependencies bool
	Force            bool
	Upgrade          bool
	DownloadDir      string // download command only
	// Installed is the copy being upgraded, if known. Its install
	// technology restricts which installers may be used (see
	// installer.SelectOptions.InstalledType).
	Installed *installed.Item
	// Migrate allows an upgrade to switch install technology (e.g. MSI →
	// MSIX) when the new version has no installer of the current one:
	// the old copy is uninstalled and the new one installed fresh.
	Migrate bool
}

// InstalledTech returns the install technology of an installed item:
// from winget-go's record, MSIX registration, or the ARP entry (Windows
// Installer products). It returns "" when unknown (e.g. a plain exe
// uninstaller), which leaves installer selection unrestricted.
func InstalledTech(it *installed.Item) string {
	switch {
	case it == nil:
		return ""
	case it.Record != nil && it.Record.InstallerType != "":
		return it.Record.InstallerType
	case it.MSIX != nil:
		return "msix"
	case it.ARP != nil && (it.ARP.WindowsInstaller ||
		strings.HasPrefix(it.ARP.KeyName, "{") && strings.Contains(strings.ToLower(it.ARP.UninstallString), "msiexec")):
		return "msi"
	}
	return ""
}

// Plan is a resolved manifest + installer (+ optional GitHub override).
type Plan struct {
	Manifest  *manifest.Manifest
	Raw       []byte
	Installer manifest.Installer
	Version   string // version that will be installed
	GitHub    *github.Override
	// MigrateFrom is set (to the installed technology family) when the
	// upgrade switches install technology; see Request.Migrate.
	MigrateFrom string
}

// selectForUpgrade picks an installer, restricted to the installed
// technology when known. With migrate, a TechMismatchError is retried
// without the restriction and the old technology is returned in
// migrateFrom. An installer of the current technology always wins.
func selectForUpgrade(m *manifest.Manifest, sel installer.SelectOptions, migrate bool) (in *manifest.Installer, migrateFrom string, err error) {
	in, err = installer.Select(m, sel)
	var tm *installer.TechMismatchError
	if !migrate || !errors.As(err, &tm) {
		return in, "", err
	}
	sel.InstalledType = ""
	if in, err = installer.Select(m, sel); err != nil {
		return nil, "", err
	}
	return in, tm.Installed, nil
}

// Plan resolves what would be installed.
func (e *Engine) Plan(ctx context.Context, r *Request) (*Plan, error) {
	m, raw, err := r.Package.Manifest(ctx, r.Version)
	if err != nil {
		return nil, err
	}
	sel := r.Select
	sel.PreferMachine = platform.IsElevated()
	if r.Upgrade && sel.InstalledType == "" {
		sel.InstalledType = InstalledTech(r.Installed)
	}
	in, migrateFrom, err := selectForUpgrade(m, sel, r.Migrate && r.Upgrade && r.Installed != nil)
	if err != nil {
		return nil, err
	}
	p := &Plan{Manifest: m, Raw: raw, Installer: *in, Version: m.PackageVersion, MigrateFrom: migrateFrom}
	if e.GitHub && r.Version == "" {
		o, err := github.Check(ctx, m, in)
		switch {
		case err != nil:
			fmt.Fprintf(e.Err, "warning: GitHub release lookup failed, using winget manifest: %v\n", err)
		case o != nil:
			p.GitHub = o
			p.Installer = o.Apply(*in)
			p.Version = o.Version
		}
	}
	return p, nil
}

// fetch downloads the plan's installer into dir and verifies its hash.
func (e *Engine) fetch(ctx context.Context, p *Plan, dir string, ignoreHash bool) (string, error) {
	in := &p.Installer
	if in.InstallerUrl == "" {
		return "", errors.New("installer has no download URL")
	}
	if !strings.HasPrefix(strings.ToLower(in.InstallerUrl), "https://") && !ignoreHash {
		fmt.Fprintf(e.Err, "warning: installer URL is not HTTPS: %s\n", in.InstallerUrl)
	}
	want := strings.ToLower(in.InstallerSha256)
	if want == "" && !ignoreHash {
		return "", errors.New("no SHA-256 is known for this installer (GitHub did not publish a digest); re-run with --ignore-security-hash to accept it unverified")
	}
	name := fileName(p)
	dst := filepath.Join(dir, name)
	if want != "" {
		if got, err := httpx.FileSHA256(dst); err == nil && got == want {
			fmt.Fprintf(e.Out, "Using cached installer %s\n", dst)
			return dst, nil
		}
	}
	fmt.Fprintf(e.Out, "Downloading %s\n", in.InstallerUrl)
	var progress = e.Err
	if fi, err := os.Stderr.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		progress = nil
	}
	got, err := httpx.Download(ctx, in.InstallerUrl, dst, progress)
	if err != nil {
		return "", err
	}
	switch {
	case want == "":
		fmt.Fprintf(e.Err, "warning: installer hash not verified (SHA-256 %s)\n", got)
	case got != want && ignoreHash:
		fmt.Fprintf(e.Err, "warning: installer hash mismatch ignored (expected %s, got %s)\n", want, got)
	case got != want:
		os.Remove(dst)
		return "", fmt.Errorf("installer hash does not match; expected %s, got %s (use --ignore-security-hash to override)", want, got)
	default:
		fmt.Fprintln(e.Out, "Successfully verified installer hash")
	}
	return dst, nil
}

var unsafeFile = regexp.MustCompile(`[^A-Za-z0-9._+-]`)

func fileName(p *Plan) string {
	parts := []string{p.Manifest.PackageIdentifier, p.Version, p.Installer.Architecture}
	if p.Installer.Scope != "" {
		parts = append(parts, p.Installer.Scope)
	}
	parts = append(parts, p.Installer.InstallerType)
	if p.Installer.InstallerLocale != "" {
		parts = append(parts, p.Installer.InstallerLocale)
	}
	return unsafeFile.ReplaceAllString(strings.Join(parts, "_"), "_") + installer.Extension(&p.Installer)
}

// describe prints the "Found ..." header and GitHub notice.
func (e *Engine) describe(p *Plan) {
	m := p.Manifest
	fmt.Fprintf(e.Out, "Found %s [%s] Version %s\n", m.Name(), m.PackageIdentifier, p.Version)
	if p.GitHub != nil {
		fmt.Fprintf(e.Out, "Using GitHub release %s (%s); the winget manifest is at %s.\n",
			p.GitHub.Release.TagName, p.GitHub.Release.HTMLURL, m.PackageVersion)
		fmt.Fprintf(e.Out, "Installer switches are taken from the %s manifest.\n", m.PackageVersion)
	}
	loc := m.Localized(installer.SystemLocale())
	if loc.License != "" || loc.LicenseUrl != "" {
		fmt.Fprintln(e.Out, "This application is licensed to you by its owner.")
		fmt.Fprintln(e.Out, "Microsoft is not responsible for, nor does it grant any licenses to, third-party packages.")
	}
}

func (e *Engine) agreements(p *Plan, accepted bool) error {
	loc := p.Manifest.Localized(installer.SystemLocale())
	if len(loc.Agreements) == 0 || accepted {
		return nil
	}
	fmt.Fprintln(e.Out, "Package agreements:")
	for _, a := range loc.Agreements {
		if a.AgreementLabel != "" {
			fmt.Fprintf(e.Out, "  %s: ", a.AgreementLabel)
		}
		fmt.Fprintf(e.Out, "%s %s\n", a.Agreement, a.AgreementUrl)
	}
	if e.confirm("Do you agree to the terms?") {
		return nil
	}
	return errors.New("package agreements were not accepted (use --accept-package-agreements)")
}

// Download fetches the installer (and manifest) into a directory. Works
// on any OS.
func (e *Engine) Download(ctx context.Context, r *Request) (string, error) {
	p, err := e.Plan(ctx, r)
	if err != nil {
		return "", err
	}
	e.describe(p)
	if err := e.agreements(p, r.AcceptAgreements); err != nil {
		return "", err
	}
	dir := r.DownloadDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Downloads")
	}
	dir = filepath.Join(dir, unsafeFile.ReplaceAllString(p.Manifest.PackageIdentifier, "_")+"_"+unsafeFile.ReplaceAllString(p.Version, "_"))
	file, err := e.fetch(ctx, p, dir, r.IgnoreHash)
	if err != nil {
		return "", err
	}
	mf := filepath.Join(dir, unsafeFile.ReplaceAllString(p.Manifest.PackageIdentifier+"_"+p.Manifest.PackageVersion, "_")+".yaml")
	if err := os.WriteFile(mf, p.Raw, 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(e.Out, "Installer downloaded: %s\n", file)
	return file, nil
}

// Install installs (or upgrades) a package. visiting guards dependency
// cycles.
func (e *Engine) Install(ctx context.Context, r *Request) error {
	return e.install(ctx, r, map[string]bool{})
}

func (e *Engine) install(ctx context.Context, r *Request, visiting map[string]bool) error {
	if !platform.IsWindows() {
		return platform.ErrUnsupported
	}
	id := strings.ToLower(r.Package.ID)
	if visiting[id] {
		return nil
	}
	visiting[id] = true

	p, err := e.Plan(ctx, r)
	if err != nil {
		return err
	}
	if !r.Force {
		if cur := e.installedVersion(r.Package.ID); cur != "" && version.Compare(cur, p.Version) >= 0 {
			fmt.Fprintf(e.Out, "%s %s is already installed (available: %s). Use --force to reinstall.\n", r.Package.ID, cur, p.Version)
			return nil
		}
	}
	if r.Upgrade && strings.EqualFold(p.Installer.UpgradeBehavior, "deny") {
		return fmt.Errorf("%s does not support upgrading in place (UpgradeBehavior: deny)", r.Package.ID)
	}
	e.describe(p)
	if err := e.agreements(p, r.AcceptAgreements); err != nil {
		return err
	}
	if !r.SkipDependencies {
		if err := e.dependencies(ctx, r, p, visiting); err != nil {
			return err
		}
	}
	dir := filepath.Join(config.CacheDir(), "installers")
	file, err := e.fetch(ctx, p, dir, r.IgnoreHash)
	if err != nil {
		return err
	}
	st, err := e.State()
	if err != nil {
		return err
	}
	migrating := p.MigrateFrom != ""
	if migrating {
		// The new installer is downloaded and verified at this point, so
		// the old copy is only removed once we know we can install.
		fmt.Fprintf(e.Out, "Migrating %s from %s to %s: uninstalling %s first...\n",
			r.Installed.Name, strings.ToUpper(p.MigrateFrom), strings.ToUpper(installer.TechFamily(p.Installer.InstallerType)), r.Installed.Label())
		if err := e.Uninstall(ctx, r.Installed, UninstallOptions{Mode: installer.ModeSilent}); err != nil {
			return fmt.Errorf("uninstall the %s copy before migrating: %w (nothing was changed)", strings.ToUpper(p.MigrateFrom), err)
		}
	} else if r.Upgrade && strings.EqualFold(p.Installer.UpgradeBehavior, "uninstallPrevious") {
		if items, _ := e.FindInstalled(r.Package.ID, true); len(items) == 1 {
			fmt.Fprintln(e.Out, "Uninstalling previous version...")
			if err := e.Uninstall(ctx, items[0], UninstallOptions{Mode: installer.ModeSilent}); err != nil {
				return fmt.Errorf("uninstall previous version: %w", err)
			}
		}
	}

	logPath := r.LogPath
	if logPath == "" {
		logDir := filepath.Join(config.DataDir(), "logs")
		_ = os.MkdirAll(logDir, 0o755)
		logPath = filepath.Join(logDir, fmt.Sprintf("%s-%s.log", unsafeFile.ReplaceAllString(r.Package.ID, "_"), time.Now().Format("20060102-150405")))
	}
	scope := r.Select.Scope
	if scope == "" {
		scope = p.Installer.Scope
	}
	fmt.Fprintln(e.Out, "Starting package install...")
	before := installed.Snapshot()
	res, err := installer.Execute(ctx, &p.Installer, file, installer.ExecOptions{
		PackageID: r.Package.ID,
		Commands:  p.Installer.Commands,
		Args: installer.ArgOptions{
			Mode: r.Mode, Scope: r.Select.Scope, InstallLocation: r.Location,
			LogPath: logPath, Override: r.Override, Custom: r.Custom, Upgrade: r.Upgrade && !migrating,
		},
	})
	if err != nil {
		if migrating {
			return fmt.Errorf("%w; the old %s copy was already uninstalled. The verified installer is %s; retry with: winget-go install --id %s -e",
				err, strings.ToUpper(p.MigrateFrom), file, r.Package.ID)
		}
		return err
	}
	rec := &installed.Record{
		ID: r.Package.ID, Name: p.Manifest.Name(), Version: p.Version,
		Source: r.Package.Source.Config.Name, InstallerType: strings.ToLower(p.Installer.InstallerType),
		Scope: scope, Architecture: p.Installer.Architecture,
		InstallLocation: res.InstallLocation, Links: res.Links,
		FromGitHub: p.GitHub != nil, InstalledAt: time.Now(),
	}
	if t := strings.ToLower(p.Installer.InstallerType); t == "zip" && p.Installer.NestedInstallerType != "" {
		rec.InstallerType = "zip/" + strings.ToLower(p.Installer.NestedInstallerType)
	}
	if old := st.Get(r.Package.ID); !migrating && old != nil && len(old.ARPKeys) > 0 && res.InstallLocation == "" {
		rec.ARPKeys = old.ARPKeys
	}
	if diff := installed.Diff(before, installed.Snapshot()); len(diff) > 0 {
		rec.ARPKeys = nil
		for _, d := range diff {
			rec.ARPKeys = append(rec.ARPKeys, d.Key)
		}
	}
	st.Put(rec)
	if err := st.Save(); err != nil {
		fmt.Fprintf(e.Err, "warning: could not save install state: %v\n", err)
	}
	fmt.Fprintln(e.Out, "Successfully installed")
	if res.PathChanged {
		fmt.Fprintln(e.Out, "PATH was updated; restart your shell to use the new command alias.")
	}
	if res.RebootRequired {
		fmt.Fprintln(e.Out, "A restart is required to complete the installation.")
	}
	return nil
}

func (e *Engine) installedVersion(id string) string {
	items, err := e.FindInstalled(id, true)
	if err != nil {
		return ""
	}
	for _, it := range items {
		if it.Package != nil && strings.EqualFold(it.Package.ID, id) {
			return it.Version
		}
	}
	return ""
}

func (e *Engine) dependencies(ctx context.Context, r *Request, p *Plan, visiting map[string]bool) error {
	d := p.Installer.Dependencies
	for _, f := range d.WindowsFeatures {
		fmt.Fprintf(e.Out, "Note: requires Windows feature %s (not installed automatically)\n", f)
	}
	for _, x := range d.ExternalDependencies {
		fmt.Fprintf(e.Out, "Note: requires external dependency %s\n", x)
	}
	for _, dep := range d.PackageDependencies {
		cur := e.installedVersion(dep.PackageIdentifier)
		if cur != "" && (dep.MinimumVersion == "" || version.Compare(cur, dep.MinimumVersion) >= 0) {
			continue
		}
		var pkg *source.Package
		for _, s := range e.Sources {
			if pkg, _ = s.Lookup(dep.PackageIdentifier); pkg != nil {
				break
			}
		}
		if pkg == nil {
			return fmt.Errorf("dependency %s not found in any source", dep.PackageIdentifier)
		}
		fmt.Fprintf(e.Out, "Installing dependency %s...\n", dep.PackageIdentifier)
		sub := *r
		sub.Package, sub.Version, sub.Upgrade = pkg, "", cur != ""
		sub.Location, sub.Override, sub.Custom, sub.LogPath = "", "", "", ""
		sub.Select.InstallerType = ""
		if err := e.install(ctx, &sub, visiting); err != nil {
			return fmt.Errorf("dependency %s: %w", dep.PackageIdentifier, err)
		}
	}
	return nil
}

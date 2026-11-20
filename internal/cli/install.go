package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"winget-go/internal/config"
	"winget-go/internal/engine"
	"winget-go/internal/exclude"
	"winget-go/internal/installed"
	"winget-go/internal/installer"
	"winget-go/internal/platform"
	"winget-go/internal/source"
)

// selectFlags choose among a manifest's installers.
type selectFlags struct {
	arch, scope, locale, itype string
}

func (s *selectFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVarP(&s.arch, "architecture", "a", "", "Select the architecture (x64, x86, arm64, arm, neutral)")
	f.StringVar(&s.scope, "scope", "", "Select install scope (user or machine)")
	f.StringVar(&s.locale, "installer-locale", "", "Locale of the installer (BCP47 format)")
	f.StringVar(&s.itype, "installer-type", "", "Select the installer type (msi, wix, exe, inno, nullsoft, burn, msix, portable, zip)")
}

func (s *selectFlags) options() installer.SelectOptions {
	return installer.SelectOptions{Architecture: s.arch, Scope: s.scope, Locale: s.locale, InstallerType: s.itype}
}

// installFlags are shared by install, upgrade and download.
type installFlags struct {
	queryFlags
	selectFlags
	version                                string
	interactive, silent                    bool
	location, log, override, custom        string
	force, ignoreHash, skipDeps, acceptPkg bool
	github                                 bool
}

func (f *installFlags) register(cmd *cobra.Command, run bool) {
	f.queryFlags.register(cmd, false)
	f.selectFlags.register(cmd)
	fl := cmd.Flags()
	fl.StringVarP(&f.version, "version", "v", "", "Use the specified version; default is the latest version")
	fl.BoolVar(&f.ignoreHash, "ignore-security-hash", false, "Ignore the installer hash check failure")
	fl.BoolVar(&f.skipDeps, "skip-dependencies", false, "Skips processing package dependencies")
	fl.BoolVar(&f.acceptPkg, "accept-package-agreements", false, "Accept all license agreements for packages")
	fl.BoolVar(&f.github, "github", config.GitHubEnabledByDefault(),
		"Use the latest GitHub release when it is newer than the winget manifest (GitHub-hosted installers only)")
	if !run {
		return
	}
	fl.BoolVarP(&f.interactive, "interactive", "i", false, "Request interactive installation; user input may be needed")
	fl.BoolVarP(&f.silent, "silent", "h", false, "Request silent installation")
	fl.StringVarP(&f.location, "location", "l", "", "Location to install to (if supported)")
	fl.StringVarP(&f.log, "log", "o", "", "Log location (if supported)")
	fl.StringVar(&f.override, "override", "", "Override arguments to be passed on to the installer")
	fl.StringVar(&f.custom, "custom", "", "Arguments to be passed on to the installer in addition to the defaults")
	fl.BoolVar(&f.force, "force", false, "Direct run the command and continue with non security related issues")
}

func (f *installFlags) request(pkg *source.Package) *engine.Request {
	mode := installer.ModeSilentWithProgress
	switch {
	case f.interactive:
		mode = installer.ModeInteractive
	case f.silent:
		mode = installer.ModeSilent
	}
	return &engine.Request{
		Package: pkg, Version: f.version, Select: f.options(), Mode: mode,
		Location: f.location, Override: f.override, Custom: f.custom, LogPath: f.log,
		AcceptAgreements: f.acceptPkg, IgnoreHash: f.ignoreHash,
		SkipDependencies: f.skipDeps, Force: f.force,
	}
}

func installCmd(o Options) *cobra.Command {
	var f installFlags
	cmd := &cobra.Command{
		Use: "install [query]", Aliases: []string{"add"},
		Short: "Installs the given package",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.empty(args) {
				return errors.New("a package query is required")
			}
			e, err := openEngine(cmd.Context(), o, f.source)
			if err != nil {
				return err
			}
			defer e.Close()
			e.GitHub = f.github
			q, _ := f.build(args, source.DefaultInstallFields)
			pkg, err := e.Resolve(q)
			if err != nil {
				return err
			}
			return e.Install(cmd.Context(), f.request(pkg))
		},
	}
	f.register(cmd, true)
	return cmd
}

func downloadCmd(o Options) *cobra.Command {
	var f installFlags
	var dir string
	cmd := &cobra.Command{
		Use:   "download [query]",
		Short: "Downloads the installer from a given package (works on any OS)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.empty(args) {
				return errors.New("a package query is required")
			}
			e, err := openEngine(cmd.Context(), o, f.source)
			if err != nil {
				return err
			}
			defer e.Close()
			e.GitHub = f.github
			q, _ := f.build(args, source.DefaultInstallFields)
			pkg, err := e.Resolve(q)
			if err != nil {
				return err
			}
			r := f.request(pkg)
			r.DownloadDir = dir
			_, err = e.Download(cmd.Context(), r)
			return err
		},
	}
	f.register(cmd, false)
	cmd.Flags().StringVarP(&dir, "download-directory", "d", "", "Directory where the installers are downloaded to (default ~/Downloads)")
	return cmd
}

func upgradeCmd(o Options) *cobra.Command {
	var f installFlags
	var ex excludeFlags
	var all, includeUnknown, migrate bool
	cmd := &cobra.Command{
		Use: "upgrade [query]", Aliases: []string{"update"},
		Short: "Shows and performs available upgrades",
		Long: `Shows and performs available upgrades.

An upgrade keeps the install technology of the installed copy (MSI, MSIX,
EXE-style or portable). If a new version is only published for a different
technology (e.g. PowerShell moving from MSI to MSIX), the upgrade stops and
explains why. Pass --migrate to switch technology: the new installer is
downloaded and verified first, then the old copy is uninstalled and the new
version installed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			e, err := openEngine(ctx, o, f.source)
			if err != nil {
				return err
			}
			defer e.Close()
			e.GitHub = f.github
			if e.Exclusions, err = ex.load(); err != nil {
				return err
			}
			ups, err := e.Upgrades(ctx, includeUnknown)
			if err != nil {
				return err
			}
			var allowed, excluded []engine.Upgrade
			for _, u := range ups {
				if u.Excluded() {
					excluded = append(excluded, u)
				} else {
					allowed = append(allowed, u)
				}
			}
			if f.empty(args) && !all {
				if len(allowed) > 0 {
					t := table{headers: []string{"Name", "Id", "Version", "Available", "Source"}}
					for _, u := range allowed {
						src := u.Item.SourceName()
						if u.ViaGitHub {
							src += " (github)"
						}
						t.add(u.Item.Name, u.Item.ID(), u.Item.Version, u.Available, src)
					}
					t.print(o.Stdout)
					fmt.Fprintf(o.Stdout, "%d upgrades available.\n", len(allowed))
				} else {
					fmt.Fprintln(o.Stdout, "No installed package found with an available upgrade.")
				}
				reportExcluded(o, excluded)
				return nil
			}
			var targets []engine.Upgrade
			if all {
				targets = allowed
				reportExcluded(o, excluded)
				if len(targets) == 0 {
					fmt.Fprintln(o.Stdout, "No installed package found with an available upgrade.")
					return nil
				}
			} else {
				term := strings.ToLower(firstNonEmpty(strings.Join(args, " "), f.id, f.name, f.moniker))
				for _, u := range ups {
					id, name := strings.ToLower(u.Item.ID()), strings.ToLower(u.Item.Name)
					if id == term || name == term || (!f.exact && (strings.Contains(id, term) || strings.Contains(name, term))) {
						targets = append(targets, u)
					}
				}
				if len(targets) == 0 {
					fmt.Fprintln(o.Stdout, "No available upgrade found.")
					return engine.ErrNoMatch
				}
				if len(targets) > 1 {
					return fmt.Errorf("multiple installed packages match %q; use --id with --exact", term)
				}
				if u := targets[0]; u.Excluded() {
					return fmt.Errorf("%s is excluded by pattern %q; pass --no-exclude to upgrade it anyway", u.Item.ID(), u.ExcludedBy)
				}
			}
			var failed []string
			for _, u := range targets {
				r := f.request(u.Item.Package)
				r.Upgrade = true
				r.Installed = u.Item
				r.Migrate = migrate
				r.Force = true // Upgrades() already established a newer version exists
				if err := e.Install(ctx, r); err != nil {
					fmt.Fprintf(o.Stderr, "%s: %v\n", u.Item.ID(), err)
					failed = append(failed, u.Item.ID())
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("%d package(s) failed to upgrade: %s", len(failed), strings.Join(failed, ", "))
			}
			return nil
		},
	}
	f.register(cmd, true)
	cmd.Flags().BoolVarP(&all, "all", "r", false, "Upgrade all installed packages to latest if available")
	cmd.Flags().Bool("recurse", false, "Alias of --all")
	cmd.Flags().BoolVarP(&includeUnknown, "include-unknown", "u", false, "Upgrade packages even if their current version cannot be determined")
	cmd.Flags().BoolVar(&migrate, "migrate", false,
		"If the new version uses a different install technology (e.g. MSI to MSIX), uninstall the old copy and install the new one")
	ex.register(cmd)
	cmd.PreRun = func(cmd *cobra.Command, args []string) {
		if v, _ := cmd.Flags().GetBool("recurse"); v {
			all = true
		}
	}
	return cmd
}

func uninstallCmd(o Options) *cobra.Command {
	var q queryFlags
	var ex excludeFlags
	var interactive, silent, all, dryRun bool
	var oldVersions string
	var keep int
	cmd := &cobra.Command{
		Use: "uninstall [query]", Aliases: []string{"remove", "rm"},
		Short: "Uninstalls the given package",
		Long: `Uninstalls the given package.

The query may be a wildcard pattern (*, ?, [...]) matched case-insensitively
against the whole Id or display name, e.g. "Microsoft.DotNet.Runtime.*" or
"Python 3.1?.*". Quote it so your shell doesn't expand it. A pattern may match
several packages; they are listed and you are asked to confirm (or pass
--disable-interactivity). Patterns honour the exclusion list.

With --old-versions, removes outdated side-by-side versions instead: installed
entries of the same product are grouped by version line and everything older
than the newest --keep versions in each line is uninstalled.

  --old-versions / --old-versions=major   3.11.4 and 3.12.1 are one line (keep 3.12.1)
  --old-versions=minor                    3.11.x and 3.12.x are separate lines
  --old-versions=all                      keep only the newest version overall

The query is optional with --old-versions; without one, every installed product
is considered (honouring the exclusion list). Use --dry-run to preview.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := installer.ModeSilentWithProgress
			if interactive {
				mode = installer.ModeInteractive
			} else if silent {
				mode = installer.ModeSilent
			}
			if cmd.Flags().Changed("old-versions") {
				return uninstallOldVersions(cmd, o, &q, &ex, args, oldVersions, keep, dryRun, mode)
			}
			if cmd.Flags().Changed("keep") || dryRun {
				return errors.New("--keep and --dry-run require --old-versions")
			}
			if q.empty(args) {
				return errors.New("a package query is required")
			}
			e, err := openEngine(cmd.Context(), o, q.source)
			if err != nil {
				return err
			}
			defer e.Close()
			term := firstNonEmpty(strings.Join(args, " "), q.id, q.name, q.moniker)
			items, err := e.FindInstalled(term, q.exact)
			if err != nil {
				return err
			}
			wildcard := engine.HasWildcard(term)
			if wildcard {
				// A pattern can sweep up more than intended, so it honours
				// the exclusion list like --old-versions does.
				if e.Exclusions, err = ex.load(); err != nil {
					return err
				}
				var excluded int
				items, excluded = filterExcluded(e, items)
				if excluded > 0 {
					fmt.Fprintf(o.Stdout, "Skipped %d installed entries matched by the exclusion list.\n", excluded)
				}
			}
			if len(items) == 0 {
				fmt.Fprintln(o.Stdout, "No installed package found matching input criteria.")
				return engine.ErrNoMatch
			}
			if len(items) > 1 {
				t := table{headers: []string{"Name", "Id", "Version", "Arch"}}
				for _, it := range items {
					t.add(it.Name, it.ID(), it.Version, it.Arch())
				}
				t.print(o.Stdout)
				if !wildcard && !all {
					return errors.New("multiple installed packages match; refine the query, pass --all-versions, or use --old-versions to remove only outdated ones")
				}
				if wildcard {
					fmt.Fprintf(o.Stdout, "%d installed package(s) match %q.\n", len(items), term)
					if err := confirmBulk(cmd, o, "Uninstall these packages?"); err != nil {
						return err
					}
				}
			}
			return uninstallEach(cmd, o, e, items, mode, "package(s)")
		},
	}
	q.register(cmd, false)
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Request interactive uninstall")
	cmd.Flags().BoolVarP(&silent, "silent", "h", false, "Request silent uninstall")
	cmd.Flags().BoolVar(&all, "all-versions", false, "Uninstall all matching entries")
	cmd.Flags().Bool("force", false, "Accepted for compatibility")
	cmd.Flags().StringVar(&oldVersions, "old-versions", "major",
		"Uninstall outdated versions, keeping the newest per version line: major, minor or all")
	cmd.Flags().Lookup("old-versions").NoOptDefVal = "major"
	cmd.Flags().IntVar(&keep, "keep", 1, "With --old-versions: number of newest versions to keep per line")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "With --old-versions: show what would be removed without uninstalling")
	ex.register(cmd)
	return cmd
}

func uninstallOldVersions(cmd *cobra.Command, o Options, q *queryFlags, ex *excludeFlags,
	args []string, lineFlag string, keep int, dryRun bool, mode installer.Mode) error {
	line, err := engine.ParseVersionLine(lineFlag)
	if err != nil {
		return err
	}
	// --old-versions has an optional value, so "--old-versions minor" parses
	// "minor" as the query. Catch that instead of searching for "minor".
	for _, a := range args {
		if _, err := engine.ParseVersionLine(a); err == nil && a != "" {
			return fmt.Errorf("use --old-versions=%s (with '='); %q was read as a package query", a, a)
		}
	}
	if keep < 1 {
		return errors.New("--keep must be at least 1")
	}
	e, err := openEngine(cmd.Context(), o, q.source)
	if err != nil {
		return err
	}
	defer e.Close()
	if e.Exclusions, err = ex.load(); err != nil {
		return err
	}
	var items []*installed.Item
	if q.empty(args) {
		items, err = e.Installed()
	} else {
		items, err = e.FindInstalled(firstNonEmpty(strings.Join(args, " "), q.id, q.name, q.moniker), q.exact)
	}
	if err != nil {
		return err
	}
	candidates, excluded := filterExcluded(e, items)
	groups := engine.PlanOldVersions(candidates, line, keep)
	if excluded > 0 {
		fmt.Fprintf(o.Stdout, "Skipped %d installed entries matched by the exclusion list.\n", excluded)
	}
	if len(groups) == 0 {
		fmt.Fprintln(o.Stdout, "No old versions found to remove.")
		return nil
	}
	t := table{headers: []string{"Name", "Id", "Version", "Arch", "Line", "Action"}}
	var targets []*installed.Item
	for _, g := range groups {
		for _, it := range g.Keep {
			t.add(it.Name, it.ID(), it.Version, it.Arch(), g.Line, "keep")
		}
		for _, it := range g.Remove {
			t.add(it.Name, it.ID(), it.Version, it.Arch(), g.Line, "REMOVE")
			targets = append(targets, it)
		}
	}
	t.print(o.Stdout)
	fmt.Fprintf(o.Stdout, "%d old version(s) to remove.\n", len(targets))
	if dryRun {
		fmt.Fprintln(o.Stdout, "Dry run: nothing was uninstalled.")
		return nil
	}
	if err := confirmBulk(cmd, o, "Uninstall these versions?"); err != nil {
		return err
	}
	return uninstallEach(cmd, o, e, targets, mode, "old version(s)")
}

// filterExcluded drops items on the engine's exclusion list and returns
// how many were dropped.
func filterExcluded(e *engine.Engine, items []*installed.Item) ([]*installed.Item, int) {
	var keep []*installed.Item
	excluded := 0
	for _, it := range items {
		id := ""
		if it.Package != nil {
			id = it.Package.ID
		}
		if _, ok := e.IsExcluded(id, it.Name); ok {
			excluded++
			continue
		}
		keep = append(keep, it)
	}
	return keep, excluded
}

// confirmBulk asks before removing several entries at once. It prompts on
// a terminal; --disable-interactivity accepts without asking. Without a
// terminal and without that flag it refuses, so a script can't remove
// more than it meant to by accident.
func confirmBulk(cmd *cobra.Command, o Options, prompt string) error {
	if noPrompt, _ := cmd.Flags().GetBool("disable-interactivity"); noPrompt {
		return nil
	}
	if !isTerminal(os.Stdin) {
		return errors.New("refusing to uninstall several entries without confirmation; pass --disable-interactivity to proceed non-interactively")
	}
	fmt.Fprintf(o.Stdout, "%s [Y/N]: ", prompt)
	var answer string
	fmt.Fscanln(os.Stdin, &answer)
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return errors.New("cancelled")
	}
	return nil
}

// uninstallEach uninstalls every item, continuing past failures and
// reporting them together. MSIX frameworks go last: Windows refuses to
// remove a framework while packages depend on it, and removes it by
// itself once the last dependent is gone.
func uninstallEach(cmd *cobra.Command, o Options, e *engine.Engine, items []*installed.Item, mode installer.Mode, what string) error {
	items = slices.Clone(items)
	slices.SortStableFunc(items, func(a, b *installed.Item) int {
		return cmp.Compare(isFramework(a), isFramework(b))
	})
	var failed []string
	inUse := 0
	for i, it := range items {
		if i > 0 {
			fmt.Fprintln(o.Stdout)
		}
		err := e.Uninstall(cmd.Context(), it, engine.UninstallOptions{Mode: mode})
		if err == nil {
			continue
		}
		if len(items) == 1 {
			return err
		}
		fmt.Fprintf(o.Stderr, "Failed: %v\n", err)
		failed = append(failed, it.Label())
		var me *platform.MSIXError
		if errors.As(err, &me) && len(me.UsedBy) > 0 {
			inUse++
		}
	}
	if len(failed) == 0 {
		return nil
	}
	fmt.Fprintf(o.Stderr, "\n%d of %d %s failed to uninstall:\n", len(failed), len(items), what)
	for _, f := range failed {
		fmt.Fprintf(o.Stderr, "  %s\n", f)
	}
	if inUse > 0 {
		fmt.Fprintf(o.Stderr, "%d of them are frameworks still used by other packages. Uninstall those packages (or leave the frameworks in place).\n", inUse)
	}
	return fmt.Errorf("%d of %d %s failed to uninstall", len(failed), len(items), what)
}

func isFramework(it *installed.Item) int {
	if it.MSIX != nil && it.MSIX.IsFramework {
		return 1
	}
	return 0
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func exportCmd(o Options) *cobra.Command {
	var out, src string
	var includeVersions bool
	cmd := &cobra.Command{
		Use: "export", Short: "Exports a list of the installed packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return errors.New("--output is required")
			}
			e, err := openEngine(cmd.Context(), o, src)
			if err != nil {
				return err
			}
			defer e.Close()
			f, err := e.Export(includeVersions)
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(f, "", "  ")
			return os.WriteFile(out, b, 0o644)
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "File where the result is to be written")
	cmd.Flags().StringVarP(&src, "source", "s", "", "Export packages from the specified source")
	cmd.Flags().BoolVar(&includeVersions, "include-versions", false, "Include package versions in export file")
	return cmd
}

func importCmd(o Options) *cobra.Command {
	var f installFlags
	var ex excludeFlags
	var file string
	var ignoreUnavailable, ignoreVersions, noUpgrade bool
	cmd := &cobra.Command{
		Use: "import", Short: "Installs all the packages in a file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return errors.New("--import-file is required")
			}
			doc, err := engine.LoadExport(file)
			if err != nil {
				return err
			}
			e, err := openEngine(cmd.Context(), o, "")
			if err != nil {
				return err
			}
			defer e.Close()
			e.GitHub = f.github
			if e.Exclusions, err = ex.load(); err != nil {
				return err
			}
			var failed []string
			for _, s := range doc.Sources {
				src := e.SourceByName(s.SourceDetails.Name)
				if src == nil {
					return fmt.Errorf("source %q from the import file is not configured", s.SourceDetails.Name)
				}
				for _, p := range s.Packages {
					if pat, ok := e.IsExcluded(p.PackageIdentifier, ""); ok {
						fmt.Fprintf(o.Stdout, "Skipping %s (excluded by %q)\n", p.PackageIdentifier, pat)
						continue
					}
					pkg, err := src.Lookup(p.PackageIdentifier)
					if err != nil || pkg == nil {
						if ignoreUnavailable {
							fmt.Fprintf(o.Stderr, "Package not found: %s\n", p.PackageIdentifier)
							continue
						}
						return fmt.Errorf("package not found: %s", p.PackageIdentifier)
					}
					r := f.request(pkg)
					if !ignoreVersions {
						r.Version = p.Version
					}
					if !noUpgrade {
						r.Upgrade = true
					}
					if err := e.Install(cmd.Context(), r); err != nil {
						fmt.Fprintf(o.Stderr, "%s: %v\n", p.PackageIdentifier, err)
						failed = append(failed, p.PackageIdentifier)
					}
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("%d package(s) failed: %s", len(failed), strings.Join(failed, ", "))
			}
			return nil
		},
	}
	f.register(cmd, false)
	cmd.Flags().StringVarP(&file, "import-file", "i", "", "File describing the packages to install")
	cmd.Flags().BoolVar(&ignoreUnavailable, "ignore-unavailable", false, "Ignore unavailable packages")
	cmd.Flags().BoolVar(&ignoreVersions, "ignore-versions", false, "Ignore package versions in import file")
	cmd.Flags().BoolVar(&noUpgrade, "no-upgrade", false, "Skips upgrade if an installed version already exists")
	ex.register(cmd)
	return cmd
}

// excludeFlags are the per-run exclusion list controls.
type excludeFlags struct {
	patterns []string
	files    []string
	disable  bool
}

func (x *excludeFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArrayVar(&x.patterns, "exclude", nil,
		"Exclude packages matching a pattern (glob on Id, or name:<glob>); repeatable or comma-separated")
	f.StringArrayVar(&x.files, "exclude-file", nil, "Read exclusion patterns from a file; repeatable")
	f.BoolVar(&x.disable, "no-exclude", false, "Ignore all exclusion lists (env, env files and flags) for this run")
}

func (x *excludeFlags) load() (*exclude.List, error) {
	return exclude.Load(exclude.Options{Patterns: x.patterns, Files: x.files, Disable: x.disable})
}

func reportExcluded(o Options, excluded []engine.Upgrade) {
	if len(excluded) == 0 {
		return
	}
	fmt.Fprintf(o.Stdout, "%d package(s) have upgrades but are excluded (use --no-exclude to include them):\n", len(excluded))
	for _, u := range excluded {
		fmt.Fprintf(o.Stdout, "  %s %s -> %s  [%s]\n", u.Item.ID(), u.Item.Version, u.Available, u.ExcludedBy)
	}
}

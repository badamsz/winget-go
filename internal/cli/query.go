package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"winget-go/internal/config"
	"winget-go/internal/engine"
	"winget-go/internal/github"
	"winget-go/internal/httpx"
	"winget-go/internal/installer"
	"winget-go/internal/source"
	"winget-go/internal/version"
)

func searchCmd(o Options) *cobra.Command {
	var q queryFlags
	var count int
	cmd := &cobra.Command{
		Use: "search [query]", Aliases: []string{"find"},
		Short: "Find and show basic info of packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openEngine(cmd.Context(), o, q.source)
			if err != nil {
				return err
			}
			defer e.Close()
			query, _ := q.build(args, source.DefaultSearchFields)
			rs, err := e.Search(query)
			if err != nil {
				return err
			}
			if len(rs) == 0 {
				fmt.Fprintln(o.Stdout, "No package found matching input criteria.")
				return engine.ErrNoMatch
			}
			t := table{headers: []string{"Name", "Id", "Version", "Match", "Source"}}
			for i, r := range rs {
				if count > 0 && i >= count {
					break
				}
				match := ""
				if r.Field >= source.FieldMoniker && !q.empty(args) {
					match = r.Field.String() + ": " + r.MatchValue
				}
				t.add(r.Name, r.ID, r.LatestVersion, match, r.Source.Config.Name)
			}
			t.print(o.Stdout)
			if count > 0 && len(rs) > count {
				fmt.Fprintf(o.Stdout, "<additional entries truncated due to result limit; %d total>\n", len(rs))
			}
			return nil
		},
	}
	q.register(cmd, true)
	cmd.Flags().IntVarP(&count, "count", "n", 0, "Show no more than specified number of results (1-1000)")
	return cmd
}

func showCmd(o Options) *cobra.Command {
	var q queryFlags
	var sel selectFlags
	var ver, locale string
	var versions, gh bool
	cmd := &cobra.Command{
		Use: "show [query]", Aliases: []string{"view"},
		Short: "Shows information about a package",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			e, err := openEngine(ctx, o, q.source)
			if err != nil {
				return err
			}
			defer e.Close()
			query, _ := q.build(args, source.DefaultInstallFields)
			pkg, err := e.Resolve(query)
			if err != nil {
				return err
			}
			if versions {
				vs, err := pkg.Versions(ctx)
				if err != nil {
					return err
				}
				fmt.Fprintf(o.Stdout, "Found %s [%s]\nVersion\n-------\n", pkg.Name, pkg.ID)
				for _, v := range vs {
					fmt.Fprintln(o.Stdout, v.Version)
				}
				return nil
			}
			m, _, err := pkg.Manifest(ctx, ver)
			if err != nil {
				return err
			}
			if locale == "" {
				locale = installer.SystemLocale()
			}
			l := m.Localized(locale)
			w := o.Stdout
			fmt.Fprintf(w, "Found %s [%s]\n", m.Name(), m.PackageIdentifier)
			kv := func(k, v string) {
				if v != "" {
					fmt.Fprintf(w, "%s: %s\n", k, strings.TrimSpace(v))
				}
			}
			block := func(k, v string) {
				if v = strings.TrimSpace(v); v != "" {
					fmt.Fprintf(w, "%s:\n", k)
					for _, line := range strings.Split(v, "\n") {
						fmt.Fprintf(w, "  %s\n", strings.TrimRight(line, "\r"))
					}
				}
			}
			kv("Version", m.PackageVersion)
			kv("Publisher", l.Publisher)
			kv("Publisher Url", l.PublisherUrl)
			kv("Publisher Support Url", l.PublisherSupportUrl)
			kv("Author", l.Author)
			kv("Moniker", l.Moniker)
			block("Description", firstNonEmpty(l.Description, l.ShortDescription))
			kv("Homepage", l.PackageUrl)
			kv("License", l.License)
			kv("License Url", l.LicenseUrl)
			kv("Privacy Url", l.PrivacyUrl)
			kv("Copyright", l.Copyright)
			block("Release Notes", l.ReleaseNotes)
			kv("Release Notes Url", l.ReleaseNotesUrl)
			kv("Purchase Url", l.PurchaseUrl)
			block("Installation Notes", l.InstallationNotes)
			if len(l.Documentations) > 0 {
				fmt.Fprintln(w, "Documentation:")
				for _, d := range l.Documentations {
					fmt.Fprintf(w, "  %s: %s\n", d.DocumentLabel, d.DocumentUrl)
				}
			}
			if len(l.Tags) > 0 {
				fmt.Fprintln(w, "Tags:")
				for _, t := range l.Tags {
					fmt.Fprintf(w, "  %s\n", t)
				}
			}
			if len(l.Agreements) > 0 {
				fmt.Fprintln(w, "Agreements:")
				for _, a := range l.Agreements {
					fmt.Fprintf(w, "  %s %s %s\n", a.AgreementLabel, a.Agreement, a.AgreementUrl)
				}
			}
			if in, err := installer.Select(m, sel.options()); err == nil {
				fmt.Fprintln(w, "Installer:")
				ikv := func(k, v string) {
					if v != "" {
						fmt.Fprintf(w, "  %s: %s\n", k, v)
					}
				}
				ikv("Installer Type", in.InstallerType)
				ikv("Nested Installer Type", in.NestedInstallerType)
				ikv("Architecture", in.Architecture)
				ikv("Scope", in.Scope)
				ikv("Installer Locale", in.InstallerLocale)
				ikv("Installer Url", in.InstallerUrl)
				ikv("Installer SHA256", in.InstallerSha256)
				ikv("Release Date", in.ReleaseDate)
				ikv("Offline Distribution Supported", "true")
				if deps := in.Dependencies.PackageDependencies; len(deps) > 0 {
					fmt.Fprintln(w, "  Dependencies:")
					for _, d := range deps {
						fmt.Fprintf(w, "    %s %s\n", d.PackageIdentifier, d.MinimumVersion)
					}
				}
			} else {
				fmt.Fprintf(w, "Installer: %v\n", err)
			}
			if gh || e.GitHub {
				rel, ref, err := github.ReleaseInfo(ctx, m)
				switch {
				case err != nil:
					fmt.Fprintf(o.Stderr, "warning: GitHub lookup failed: %v\n", err)
				case rel != nil:
					fmt.Fprintf(w, "GitHub Release (%s/%s):\n", ref.Owner, ref.Repo)
					fmt.Fprintf(w, "  Latest Tag: %s", rel.TagName)
					if v := github.TagVersion(rel.TagName); version.Compare(v, m.PackageVersion) > 0 {
						fmt.Fprintf(w, "  (newer than winget manifest %s)", m.PackageVersion)
					}
					fmt.Fprintln(w)
					fmt.Fprintf(w, "  Published: %s\n  Url: %s\n", rel.PublishedAt, rel.HTMLURL)
					if body := strings.TrimSpace(rel.Body); body != "" {
						lines := strings.Split(body, "\n")
						if len(lines) > 25 {
							lines = append(lines[:25], "...")
						}
						fmt.Fprintln(w, "  Notes:")
						for _, ln := range lines {
							fmt.Fprintf(w, "    %s\n", strings.TrimRight(ln, "\r"))
						}
					}
				}
			}
			return nil
		},
	}
	q.register(cmd, false)
	sel.register(cmd)
	cmd.Flags().StringVarP(&ver, "version", "v", "", "Use the specified version; default is the latest version")
	cmd.Flags().StringVar(&locale, "locale", "", "Locale to use (BCP47 format)")
	cmd.Flags().BoolVar(&versions, "versions", false, "Show available versions of the package")
	cmd.Flags().BoolVar(&gh, "github", config.GitHubEnabledByDefault(), "Also show the latest GitHub release for GitHub-hosted installers")
	return cmd
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func listCmd(o Options) *cobra.Command {
	var q queryFlags
	var ex excludeFlags
	var count int
	var upgradeAvailable, includeUnknown, gh bool
	cmd := &cobra.Command{
		Use: "list [query]", Aliases: []string{"ls"},
		Short: "Display installed packages",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openEngine(cmd.Context(), o, q.source)
			if err != nil {
				return err
			}
			defer e.Close()
			e.GitHub = gh
			if e.Exclusions, err = ex.load(); err != nil {
				return err
			}
			// Always compute availability so the Available column is filled.
			avail := map[string]string{}
			ups, err := e.Upgrades(cmd.Context(), includeUnknown)
			if err != nil {
				return err
			}
			for _, u := range ups {
				a := u.Available
				if u.Excluded() {
					if upgradeAvailable {
						continue
					}
					a += " (excluded)"
				}
				avail[u.Item.ID()+"|"+u.Item.Version] = a
			}
			items, err := e.Installed()
			if err != nil {
				return err
			}
			term := strings.ToLower(firstNonEmpty(strings.Join(args, " "), q.id, q.name, q.moniker))
			t := table{headers: []string{"Name", "Id", "Version", "Available", "Source"}}
			n := 0
			for _, it := range items {
				a := avail[it.ID()+"|"+it.Version]
				if upgradeAvailable && a == "" {
					continue
				}
				if term != "" {
					hay := strings.ToLower(it.Name + " " + it.ID())
					if q.exact {
						if !strings.EqualFold(it.ID(), term) && !strings.EqualFold(it.Name, term) {
							continue
						}
					} else if !strings.Contains(hay, term) {
						continue
					}
				}
				if count > 0 && n >= count {
					break
				}
				n++
				t.add(it.Name, it.ID(), firstNonEmpty(it.Version, "Unknown"), a, it.SourceName())
			}
			if n == 0 {
				fmt.Fprintln(o.Stdout, "No installed package found matching input criteria.")
				return engine.ErrNoMatch
			}
			t.print(o.Stdout)
			return nil
		},
	}
	q.register(cmd, false)
	cmd.Flags().IntVarP(&count, "count", "n", 0, "Show no more than specified number of results")
	cmd.Flags().BoolVar(&upgradeAvailable, "upgrade-available", false, "Lists only packages which have an upgrade available")
	cmd.Flags().BoolVarP(&includeUnknown, "include-unknown", "u", false, "List packages even if their current version cannot be determined")
	cmd.Flags().BoolVar(&gh, "github", config.GitHubEnabledByDefault(), "Check GitHub Releases for newer versions of GitHub-hosted packages")
	ex.register(cmd)
	return cmd
}

func hashCmd(o Options) *cobra.Command {
	return &cobra.Command{
		Use:   "hash <file>",
		Short: "Helper to hash installer files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := httpx.FileSHA256(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "InstallerSha256: %s\n", strings.ToUpper(h))
			return nil
		},
	}
}

func sourceCmd(o Options) *cobra.Command {
	cmd := &cobra.Command{Use: "source", Short: "Manage sources of packages"}
	list := &cobra.Command{
		Use: "list [name]", Aliases: []string{"ls"}, Short: "List current sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgs, err := config.LoadSources()
			if err != nil {
				return err
			}
			t := table{headers: []string{"Name", "Argument", "Explicit"}}
			for _, c := range cfgs {
				if len(args) > 0 && !strings.EqualFold(args[0], c.Name) {
					continue
				}
				t.add(c.Name, c.Arg, fmt.Sprint(c.Explicit))
			}
			t.print(o.Stdout)
			return nil
		},
	}
	update := &cobra.Command{
		Use: "update [name]", Aliases: []string{"refresh"}, Short: "Update current sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgs, err := config.LoadSources()
			if err != nil {
				return err
			}
			var failed error
			for _, c := range cfgs {
				if len(args) > 0 && !strings.EqualFold(args[0], c.Name) {
					continue
				}
				fmt.Fprintf(o.Stdout, "Updating source: %s...\n", c.Name)
				s, err := source.Open(cmd.Context(), c, o.Stderr)
				if err == nil {
					err = s.Update(cmd.Context(), true)
					s.Close()
				}
				if err != nil {
					fmt.Fprintf(o.Stderr, "  failed: %v\n", err)
					failed = err
					continue
				}
				fmt.Fprintln(o.Stdout, "Done")
			}
			return failed
		},
	}
	var addName, addArg, addType string
	var explicit bool
	add := &cobra.Command{
		Use: "add", Short: "Add a new source",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && addName == "" {
				addName = args[0]
			}
			if len(args) > 1 && addArg == "" {
				addArg = args[1]
			}
			if addName == "" || addArg == "" {
				return fmt.Errorf("--name and --arg are required")
			}
			if addType == "" {
				addType = config.PreIndexedType
			}
			if addType != config.PreIndexedType {
				return fmt.Errorf("source type %q is not supported (only %s)", addType, config.PreIndexedType)
			}
			cfgs, err := config.LoadSources()
			if err != nil {
				return err
			}
			for _, c := range cfgs {
				if strings.EqualFold(c.Name, addName) {
					return fmt.Errorf("a source named %q already exists", addName)
				}
			}
			c := config.SourceConfig{Name: addName, Arg: strings.TrimRight(addArg, "/"), Type: addType, Explicit: explicit}
			s, err := source.Open(cmd.Context(), c, o.Stderr)
			if err != nil {
				return fmt.Errorf("source validation failed: %w", err)
			}
			s.Close()
			if err := config.SaveSources(append(cfgs, c)); err != nil {
				return err
			}
			fmt.Fprintln(o.Stdout, "Done")
			return nil
		},
	}
	add.Flags().StringVarP(&addName, "name", "n", "", "Name of the source")
	add.Flags().StringVarP(&addArg, "arg", "a", "", "Argument given to the source (base URL)")
	add.Flags().StringVarP(&addType, "type", "t", "", "Type of the source")
	add.Flags().BoolVar(&explicit, "explicit", false, "Exclude from discovery unless specified with --source")
	remove := &cobra.Command{
		Use: "remove <name>", Aliases: []string{"rm"}, Short: "Remove current sources", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgs, err := config.LoadSources()
			if err != nil {
				return err
			}
			var keep []config.SourceConfig
			for _, c := range cfgs {
				if strings.EqualFold(c.Name, args[0]) {
					_ = os.RemoveAll(source.Dir(c))
					continue
				}
				keep = append(keep, c)
			}
			if len(keep) == len(cfgs) {
				return fmt.Errorf("no source named %q", args[0])
			}
			if keep == nil {
				keep = []config.SourceConfig{}
			}
			return config.SaveSources(keep)
		},
	}
	reset := &cobra.Command{
		Use: "reset", Short: "Reset sources to the default",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgs, _ := config.LoadSources()
			for _, c := range cfgs {
				_ = os.RemoveAll(source.Dir(c))
			}
			if err := config.ResetSources(); err != nil {
				return err
			}
			fmt.Fprintln(o.Stdout, "Resetting all sources...Done")
			return nil
		},
	}
	export := &cobra.Command{
		Use: "export", Short: "Export current sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgs, err := config.LoadSources()
			if err != nil {
				return err
			}
			sort.Slice(cfgs, func(i, j int) bool { return cfgs[i].Name < cfgs[j].Name })
			for _, c := range cfgs {
				fmt.Fprintf(o.Stdout, `{"Arg":%q,"Data":"","Explicit":%v,"Identifier":%q,"Name":%q,"TrustLevel":["None"],"Type":%q}`+"\n",
					c.Arg, c.Explicit, c.Identifier, c.Name, c.Type)
			}
			return nil
		},
	}
	cmd.AddCommand(list, update, add, remove, reset, export)
	return cmd
}

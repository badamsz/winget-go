// Package cli implements winget-go's command line for the portable
// backend, mirroring winget's commands and flag names.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"winget-go/internal/config"
	"winget-go/internal/engine"
	"winget-go/internal/exclude"
	"winget-go/internal/installer"
	"winget-go/internal/platform"
	"winget-go/internal/source"
)

// ExitNoApplicationsFound is winget's APPINSTALLER_CLI_ERROR_NO_APPLICATIONS_FOUND.
const ExitNoApplicationsFound = 0x8A150014

// ExitCode maps an error to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, engine.ErrNoMatch) && platform.IsWindows() {
		code := uint32(ExitNoApplicationsFound)
		return int(int32(code))
	}
	return 1
}

// Options are process-level settings provided by main.
type Options struct {
	Backend    string // resolved backend description, for --info
	NativePath string
	EnvFiles   []string // winget-go.env files that were loaded
	Stdout     io.Writer
	Stderr     io.Writer
}

// NewRoot builds the command tree.
func NewRoot(o Options) *cobra.Command {
	var showVersion, info bool
	root := &cobra.Command{
		Use:           "winget-go",
		Short:         "A cross-platform Windows Package Manager client written in Go",
		Long:          "winget-go is a Go implementation of the winget client. It uses the same community\nsource as winget and can run on systems without winget (e.g. Windows Server, Sandbox,\nor macOS/Linux for search, show and download).",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case showVersion:
				fmt.Fprintf(o.Stdout, "v%s\n", config.Version)
				return nil
			case info:
				fmt.Fprintf(o.Stdout, "winget-go v%s\n", config.Version)
				fmt.Fprintf(o.Stdout, "Backend:      %s\n", o.Backend)
				if o.NativePath != "" {
					fmt.Fprintf(o.Stdout, "Native winget: %s\n", o.NativePath)
				}
				fmt.Fprintf(o.Stdout, "Cache:        %s\n", config.CacheDir())
				fmt.Fprintf(o.Stdout, "Config:       %s\n", config.ConfigDir())
				fmt.Fprintf(o.Stdout, "Data:         %s\n", config.DataDir())
				fmt.Fprintf(o.Stdout, "Links (PATH): %s\n", installer.LinksDir())
				fmt.Fprintf(o.Stdout, "Elevated:     %v\n", platform.IsElevated())
				fmt.Fprintln(o.Stdout, "Env files (searched in precedence order):")
				for _, f := range config.EnvFileCandidates() {
					state := "not found"
					if slices.Contains(o.EnvFiles, f) {
						state = "loaded"
					}
					fmt.Fprintf(o.Stdout, "  %s [%s]\n", f, state)
				}
				if l, err := exclude.Load(exclude.Options{}); err != nil {
					fmt.Fprintf(o.Stdout, "Exclusions:   error: %v\n", err)
				} else {
					fmt.Fprintf(o.Stdout, "Exclusions:   %d pattern(s)\n", l.Len())
					for _, p := range l.Patterns() {
						fmt.Fprintf(o.Stdout, "  %s\n", p)
					}
				}
				return nil
			}
			return cmd.Help()
		},
	}
	root.SetOut(o.Stdout)
	root.SetErr(o.Stderr)
	// winget uses -h for --silent, so help has no shorthand.
	root.PersistentFlags().Bool("help", false, "Show help")
	root.Flags().BoolVarP(&showVersion, "version", "v", false, "Display the version of the tool")
	root.Flags().BoolVar(&info, "info", false, "Display general info of the tool")
	root.PersistentFlags().Bool("disable-interactivity", false, "Disable interactive prompts")
	root.PersistentFlags().Bool("accept-source-agreements", false, "Accept all source agreements (accepted for compatibility)")

	root.AddCommand(
		searchCmd(o), showCmd(o), listCmd(o), sourceCmd(o), hashCmd(o),
		installCmd(o), upgradeCmd(o), uninstallCmd(o), downloadCmd(o),
		exportCmd(o), importCmd(o),
	)
	for _, name := range []string{"configure", "pin", "repair", "settings", "features", "validate", "dscv3", "mcp"} {
		n := name
		root.AddCommand(&cobra.Command{
			Use: n, Short: "Requires native winget", Hidden: true,
			DisableFlagParsing: true,
			RunE: func(*cobra.Command, []string) error {
				return fmt.Errorf("`%s` is only available with the native winget backend", n)
			},
		})
	}
	return root
}

// queryFlags are the package-selection flags shared by most commands.
type queryFlags struct {
	id, name, moniker, tag, command string
	exact                           bool
	source                          string
}

func (q *queryFlags) register(cmd *cobra.Command, withTagCommand bool) {
	f := cmd.Flags()
	f.StringVar(&q.id, "id", "", "Filter results by id")
	f.StringVar(&q.name, "name", "", "Filter results by name")
	f.StringVar(&q.moniker, "moniker", "", "Filter results by moniker")
	if withTagCommand {
		f.StringVar(&q.tag, "tag", "", "Filter results by tag")
		f.StringVar(&q.command, "command", "", "Filter results by command")
		f.StringVar(&q.command, "cmd", "", "Filter results by command")
		f.Lookup("cmd").Hidden = true
	}
	f.BoolVarP(&q.exact, "exact", "e", false, "Find package using exact match")
	f.StringVarP(&q.source, "source", "s", "", "Find package using the specified source")
}

func (q *queryFlags) build(args []string, defaults []source.Field) (source.Query, error) {
	term := strings.Join(args, " ")
	switch {
	case q.id != "":
		return source.Query{Term: q.id, Fields: []source.Field{source.FieldID}, Exact: q.exact}, nil
	case q.name != "":
		return source.Query{Term: q.name, Fields: []source.Field{source.FieldName}, Exact: q.exact}, nil
	case q.moniker != "":
		return source.Query{Term: q.moniker, Fields: []source.Field{source.FieldMoniker}, Exact: q.exact}, nil
	case q.tag != "":
		return source.Query{Term: q.tag, Fields: []source.Field{source.FieldTag}, Exact: q.exact}, nil
	case q.command != "":
		return source.Query{Term: q.command, Fields: []source.Field{source.FieldCommand}, Exact: q.exact}, nil
	}
	return source.Query{Term: term, Fields: defaults, Exact: q.exact}, nil
}

func (q *queryFlags) empty(args []string) bool {
	return len(args) == 0 && q.id == "" && q.name == "" && q.moniker == "" && q.tag == "" && q.command == ""
}

func openEngine(ctx context.Context, o Options, src string) (*engine.Engine, error) {
	return engine.New(ctx, src, o.Stdout, o.Stderr)
}

// table renders winget-style tables.
type table struct {
	headers []string
	rows    [][]string
}

func (t *table) add(cols ...string) { t.rows = append(t.rows, cols) }

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func (t *table) print(w io.Writer) {
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, r := range t.rows {
		for i, c := range r {
			r[i] = trunc(c, 48)
			if n := utf8.RuneCountInString(r[i]); n > widths[i] {
				widths[i] = n
			}
		}
	}
	line := func(cols []string) {
		var b strings.Builder
		for i, c := range cols {
			b.WriteString(c)
			if i < len(cols)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)+1))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	line(t.headers)
	total := len(widths) - 1
	for _, n := range widths {
		total += n
	}
	fmt.Fprintln(w, strings.Repeat("-", total))
	for _, r := range t.rows {
		line(r)
	}
}

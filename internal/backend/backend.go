// Package backend decides whether a command is executed by the native
// winget client (passthrough) or by winget-go's own Go implementation.
package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"winget-go/internal/platform"
)

// Kind is a backend choice.
type Kind string

const (
	Auto     Kind = "auto"
	Native   Kind = "native"
	Portable Kind = "portable"
)

// goOnlyFlags only exist in winget-go; their presence forces the
// portable backend in auto mode.
var goOnlyFlags = []string{"--github", "--exclude", "--exclude-file", "--no-exclude", "--old-versions", "--keep", "--dry-run"}

// Commands that honour exclusion lists run on the portable backend in
// auto mode when exclusions are configured, because native winget knows
// nothing about them.
// usesExclusions reports whether a command line is affected by exclusions
// (`list` only with --upgrade-available).
func usesExclusions(args []string) bool {
	switch Command(args) {
	case "upgrade", "update", "import":
		return true
	case "list", "ls":
		for _, a := range args {
			if a == "--upgrade-available" {
				return true
			}
		}
	}
	return false
}

// usesWildcardUninstall reports whether an uninstall query (positional or
// flag value) is a glob pattern. Native winget would treat "*" literally,
// so winget-go handles these itself.
func usesWildcardUninstall(args []string) bool {
	switch Command(args) {
	case "uninstall", "remove", "rm":
	default:
		return false
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if i := strings.IndexByte(a, '='); strings.HasPrefix(a, "--") && i > 0 {
			a = a[i+1:]
		} else if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.ContainsAny(a, "*?[") {
			return true
		}
	}
	return false
}

// Hints carries configuration that influences auto selection.
type Hints struct {
	ExclusionsConfigured bool
}

// Command returns the first non-flag argument (the subcommand).
func Command(args []string) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			return strings.ToLower(a)
		}
	}
	return ""
}

// Decision is the resolved backend.
type Decision struct {
	Kind       Kind
	NativePath string
	Reason     string
	Args       []string // args with --backend removed
	// Warning is set when a configured feature will not apply (e.g.
	// exclusions with an explicitly requested native backend).
	Warning string
}

// ExtractFlag removes --backend / --backend=X from args.
func ExtractFlag(args []string) (string, []string, error) {
	var out []string
	val := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			out = append(out, args[i:]...)
			return val, out, nil
		case a == "--backend":
			if i+1 >= len(args) {
				return "", nil, errors.New("--backend requires a value (auto, native, portable)")
			}
			val = args[i+1]
			i++
		case strings.HasPrefix(a, "--backend="):
			val = strings.TrimPrefix(a, "--backend=")
		default:
			out = append(out, a)
		}
	}
	return val, out, nil
}

// Decide resolves the backend from the flag, WINGET_GO_BACKEND and the
// environment. find locates native winget (injectable for tests).
func Decide(args []string, find func() string, h Hints) (*Decision, error) {
	flag, rest, err := ExtractFlag(args)
	if err != nil {
		return nil, err
	}
	want := Kind(strings.ToLower(flag))
	if want == "" {
		want = Kind(strings.ToLower(os.Getenv("WINGET_GO_BACKEND")))
	}
	if want == "" {
		want = Auto
	}
	d := &Decision{Args: rest}
	switch want {
	case Portable:
		d.Kind, d.Reason = Portable, "requested"
		return d, nil
	case Native:
		p := find()
		if p == "" {
			return nil, errors.New("native winget was requested but winget.exe could not be found")
		}
		d.Kind, d.NativePath, d.Reason = Native, p, "requested"
		if h.ExclusionsConfigured && usesExclusions(rest) {
			d.Warning = "exclusion list is ignored by the native winget backend"
		}
		if usesWildcardUninstall(rest) {
			d.Warning = "native winget does not support wildcard uninstall queries; the pattern is matched literally"
		}
		return d, nil
	case Auto:
	default:
		return nil, fmt.Errorf("unknown backend %q (use auto, native or portable)", want)
	}
	for _, a := range rest {
		for _, f := range goOnlyFlags {
			if a == f || strings.HasPrefix(a, f+"=") {
				d.Kind, d.Reason = Portable, f+" is a winget-go feature"
				return d, nil
			}
		}
	}
	if h.ExclusionsConfigured && usesExclusions(rest) {
		d.Kind, d.Reason = Portable, "exclusion list applies to `"+Command(rest)+"`"
		return d, nil
	}
	if usesWildcardUninstall(rest) {
		d.Kind, d.Reason = Portable, "wildcard uninstall is a winget-go feature"
		return d, nil
	}
	if p := find(); p != "" {
		d.Kind, d.NativePath, d.Reason = Native, p, "native winget found"
		return d, nil
	}
	d.Kind = Portable
	if platform.IsWindows() {
		d.Reason = "native winget not found"
	} else {
		d.Reason = "non-Windows host"
	}
	return d, nil
}

// Passthrough runs native winget with args, wiring stdio, and returns
// its exit code.
func Passthrough(path string, args []string) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

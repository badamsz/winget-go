// Command winget-go is a Go implementation of the Windows Package Manager
// client. On Windows with winget installed it transparently delegates to
// the native client; elsewhere (Windows Server/LTSC/Sandbox without App
// Installer, SYSTEM contexts, or macOS/Linux) it uses its own pure-Go
// implementation of the winget community source.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"winget-go/internal/backend"
	"winget-go/internal/cli"
	"winget-go/internal/config"
	"winget-go/internal/exclude"
	"winget-go/internal/platform"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Env files may set WINGET_GO_BACKEND, so load them before deciding.
	envFiles, err := config.LoadEnvFiles()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	d, err := backend.Decide(os.Args[1:], platform.FindNativeWinget,
		backend.Hints{ExclusionsConfigured: exclude.Configured()})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	if d.Warning != "" {
		fmt.Fprintln(os.Stderr, "warning:", d.Warning)
	}
	if os.Getenv("WINGET_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[winget-go] backend=%s (%s) %s\n", d.Kind, d.Reason, d.NativePath)
	}
	if d.Kind == backend.Native {
		code, err := backend.Passthrough(d.NativePath, d.Args)
		if err == nil {
			return code
		}
		// The alias exists but cannot start (e.g. broken App Installer):
		// fall back to the Go implementation.
		fmt.Fprintf(os.Stderr, "warning: native winget failed to start (%v); using winget-go\n", err)
		d.Reason = "native winget failed to start"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := cli.NewRoot(cli.Options{
		Backend:    fmt.Sprintf("portable (%s)", d.Reason),
		NativePath: d.NativePath,
		EnvFiles:   envFiles,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	})
	root.SetArgs(d.Args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return cli.ExitCode(err)
	}
	return 0
}

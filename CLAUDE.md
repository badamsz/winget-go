# winget-go

Go implementation of the winget client. See `docs/ARCHITECTURE.md` for the design.

## Rules

- Keep `docs/ARCHITECTURE.md` in sync: any change to packages, commands, backend selection, source/manifest handling, installer behaviour, GitHub integration, file locations, CI or Renovate must update the matching section in the same change.
- Keep OS-specific code in `internal/platform` (`*_windows.go` / `*_other.go`). Other packages must compile on every OS.
- Before finishing: `gofmt -l .` (must be empty), `go vet ./...`, `GOOS=windows go vet ./...`, `go test ./...`.
- Tests must stay offline (use `httptest`; see `internal/source/source_test.go`).

# winget-go

A Go implementation of the Windows Package Manager client ([winget](https://github.com/microsoft/winget-cli)).

* **On Windows with winget**, it passes your command straight to the native `winget.exe`.
* **On Windows without winget** (Server, LTSC, Sandbox, Server Core, `SYSTEM` contexts), it installs, upgrades, lists and uninstalls packages itself, using the same community repository.
* **On macOS/Linux**, it can search, show and download Windows installers (verified by SHA-256), for example to stage offline installs.
* **Optionally**, with `--github`, it uses the latest GitHub release of GitHub-hosted packages when that release is newer than the winget manifest.

It ships as a single static binary with no runtime dependencies.

## Install

Download a binary from the [releases](../../releases) page, or build it:

```sh
go build -o winget-go ./cmd/winget-go          # host
GOOS=windows GOARCH=amd64 go build -o winget-go.exe ./cmd/winget-go
```

## Usage

The commands and flags match winget's:

```sh
winget-go search 7zip
winget-go show --id 7zip.7zip -e
winget-go install --id 7zip.7zip -e --silent
winget-go list --upgrade-available
winget-go upgrade --all
winget-go uninstall 7zip.7zip
winget-go download --id jqlang.jq -e -d ./installers -a arm64
winget-go export -o packages.json && winget-go import -i packages.json
winget-go source update
```

Unlike winget, `-h` means `--silent`, so use `--help` for help.

### Choosing a backend

| | |
|---|---|
| `--backend auto` (default) | Native winget if it is found; otherwise the Go implementation |
| `--backend native` | Always native winget (error if missing) |
| `--backend portable` | Always the Go implementation |

You can also set the backend with `WINGET_GO_BACKEND`. `WINGET_GO_DEBUG=1` prints which backend was chosen. Passing `--github` selects the Go implementation automatically.

### GitHub releases

```sh
winget-go show --id jqlang.jq -e --github     # show latest release info
winget-go install --id Foo.Bar -e --github    # install a newer GitHub release if one exists
winget-go upgrade --github                    # include GitHub releases in upgrade checks
```

The installer hash comes from GitHub's published asset digest. If GitHub has no digest for an asset, winget-go refuses to install it unless you pass `--ignore-security-hash`. Set `GITHUB_TOKEN` to raise the API rate limit.

### Exclusions

You can stop specific packages from being upgraded. `upgrade`, `upgrade --all`, `list --upgrade-available` and `import` all skip excluded packages:

```sh
winget-go upgrade --all --exclude "Microsoft.Edge*" --exclude Google.Chrome
winget-go upgrade --all --exclude-file ./exclusions.txt
winget-go upgrade --all --no-exclude          # ignore every exclusion for this run
```

Patterns are case-insensitive wildcards (`*`, `?`, `[...]`) matched against the package Id. Start a pattern with `name:` to match the display name instead, e.g. `name:Visual Studio *`. An excluded package still appears in `list` (marked `(excluded)`) and in a footer after `upgrade`. Asking to upgrade it by name fails until you pass `--no-exclude`.

To avoid passing flags on every run, put the list in a **`winget-go.env`** file. winget-go finds it automatically; see [`winget-go.env.example`](winget-go.env.example):

```sh
WINGET_GO_EXCLUDE="
  Microsoft.Edge*
  Google.Chrome*   # updates itself
"
```

The files are searched in this order (highest precedence first):

1. The user config folder: `%APPDATA%\winget-go\`, `~/Library/Application Support/winget-go/` or `~/.config/winget-go/`
2. The machine-wide folder: `%ProgramData%\winget-go\` or `/etc/winget-go/`
3. The folder next to the `winget-go` executable

Set `WINGET_GO_ENV_FILE` to use exactly one file instead. Exclusions from every file, the environment and the flags are combined. For other settings, a real environment variable beats the file. Env files may only set `WINGET_GO_*`, `GITHUB_TOKEN` and `GH_TOKEN`. To see which files were loaded and which patterns are active, run `winget-go --backend portable --info`.

Native winget doesn't support exclusions. So when exclusions are configured, `upgrade`, `import` and `list --upgrade-available` use the Go implementation even if winget is installed. Forcing `--backend native` prints a warning that the exclusions are being ignored.

### Environment

| Variable | Purpose |
|---|---|
| `WINGET_GO_BACKEND` | `auto`, `native` or `portable` |
| `WINGET_GO_EXCLUDE` | Exclusion patterns (comma-, semicolon- or newline-separated) |
| `WINGET_GO_EXCLUDE_FILE` | Exclusion list file(s), separated by `;` on Windows or `:` elsewhere |
| `WINGET_GO_ENV_FILE` | Load exactly this settings file instead of searching |
| `WINGET_GO_GITHUB=1` | Turn on `--github` by default |
| `WINGET_GO_AUTOUPDATE_MINUTES` | Source refresh interval (default 15; 0 turns it off) |
| `WINGET_GO_HOME` | Move the cache, config and data directories |
| `WINGET_GO_LOCALE` | Preferred installer/metadata locale |
| `GITHUB_TOKEN` / `GH_TOKEN` | GitHub API authentication |

## Documentation

* [Architecture](docs/ARCHITECTURE.md): design, data formats, install pipeline and known gaps. Update it with every structural change.

## Development

```sh
go test ./...                 # offline; uses a fake source server
GOOS=windows go vet ./...     # check the Windows build
```

CI runs on Linux, Windows and macOS and builds binaries for every target. Pushing a `v*` tag publishes a release. Renovate keeps Go modules, the toolchain and Actions up to date.

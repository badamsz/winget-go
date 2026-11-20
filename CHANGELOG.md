# Changelog

All notable changes to winget-go are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). The release workflow publishes the
section for each tag as its GitHub release notes.

## [1.0.0] - 2026-11-20

First stable release. winget-go is a single static binary that speaks
winget's commands and flags. With native winget installed, it passes commands
straight through. Without it (Windows Server, LTSC, Sandbox, `SYSTEM`
contexts, or macOS/Linux), it does the work itself against the same
community repository.

### Commands

- `search`, `show`, `list`, `install`, `upgrade`, `uninstall`, `download`,
  `hash`, `export`, `import` and `source` (`list`, `update`, `add`, `export`,
  `remove`, `reset`), with winget's flags.
- Reads the winget community source (the pre-indexed `source2.msix`). The
  index refreshes automatically, and manifests are verified by SHA-256.
- Installs MSI/WiX, MSIX, EXE (Inno, Nullsoft, Burn), portable and zip
  packages. It follows dependencies, elevates through UAC when needed, and
  tracks what it installed.
- Lists installed apps from Apps & Features and MSIX packages, matched to
  winget Ids by product code, package family name or normalized name. Ids
  match native winget's format (`ARP\Machine\X64\…`, `MSIX\…`).
- On macOS/Linux: `search`, `show`, `download` (hash-verified) and `hash`,
  for example to stage offline installs.

### Beyond winget

- `--github`: use a newer GitHub release of a GitHub-hosted package before
  its manifest is updated, verified by GitHub's published asset digest.
- Exclusion lists (`--exclude`, `--exclude-file`, `WINGET_GO_EXCLUDE`) that
  keep packages out of `upgrade`, `import` and bulk uninstalls. A
  `winget-go.env` settings file is found automatically.
- `uninstall --old-versions[=major|minor|all]`: remove outdated
  side-by-side versions, keeping the newest in each version line. Supports
  `--keep N` and `--dry-run`.
- Wildcard queries for `uninstall` and `upgrade`, e.g.
  `upgrade "Microsoft.VCRedist.*"`.
- Upgrades keep the installed copy's install technology (MSI, MSIX, EXE,
  portable), as winget does. `upgrade --migrate` switches technology when a
  publisher changes it (e.g. PowerShell 7.7 moving from MSI to MSIX): the
  new installer is verified first, then the old copy is replaced.
- `--backend auto|native|portable`. Auto picks the Go implementation for
  features native winget lacks.

### Known limitations

- No `configure` (DSC), `pin`, `repair` or `settings` in the Go
  implementation. No Microsoft Store (`msstore`) or REST sources.
- MSIX handling is per-user. MSIX versions are shown raw and are not
  offered as upgrades.
- The signature of `source2.msix` itself is not verified. Manifests are
  verified against the hashes in the index.
- Install and uninstall flows have had limited testing on real Windows
  machines. Please report problems.

[1.0.0]: https://github.com/badamsz/winget-go/releases/tag/v1.0.0

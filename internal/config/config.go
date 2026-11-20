// Package config resolves on-disk locations and the configured sources.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Version of winget-go; release builds set it with
// -ldflags "-X winget-go/internal/config.Version=<tag>".
var Version = "0.1.0-dev"

// DefaultSourceName and DefaultSourceArg describe the community repository.
const (
	DefaultSourceName = "winget"
	DefaultSourceArg  = "https://cdn.winget.microsoft.com/cache"
	PreIndexedType    = "Microsoft.PreIndexed.Package"
	DefaultSourceID   = "Microsoft.Winget.Source_8wekyb3d8bbwe"
)

// SourceConfig is one configured package source.
type SourceConfig struct {
	Name       string `json:"Name"`
	Type       string `json:"Type"`
	Arg        string `json:"Arg"`
	Identifier string `json:"Identifier"`
	Explicit   bool   `json:"Explicit,omitempty"`
}

func home() string { return os.Getenv("WINGET_GO_HOME") }

// CacheDir holds downloaded indexes, version data and manifests.
func CacheDir() string {
	if h := home(); h != "" {
		return filepath.Join(h, "cache")
	}
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "winget-go")
}

// ConfigDir holds sources.json.
func ConfigDir() string {
	if h := home(); h != "" {
		return filepath.Join(h, "config")
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "winget-go")
}

// DataDir holds installed-package state, portable packages and links.
func DataDir() string {
	if h := home(); h != "" {
		return filepath.Join(h, "data")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "winget-go")
		}
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "winget-go")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "winget-go")
}

// AutoUpdateInterval is how stale a source index may get before it is
// refreshed automatically (WINGET_GO_AUTOUPDATE_MINUTES, default 15; 0
// disables automatic updates).
func AutoUpdateInterval() time.Duration {
	if v := os.Getenv("WINGET_GO_AUTOUPDATE_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Minute
		}
	}
	return 15 * time.Minute
}

// GitHubEnabledByDefault reports whether WINGET_GO_GITHUB enables the
// GitHub releases lookup without passing --github.
func GitHubEnabledByDefault() bool {
	switch strings.ToLower(os.Getenv("WINGET_GO_GITHUB")) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func sourcesFile() string { return filepath.Join(ConfigDir(), "sources.json") }

// DefaultSources returns the built-in source list.
func DefaultSources() []SourceConfig {
	return []SourceConfig{{
		Name: DefaultSourceName, Type: PreIndexedType,
		Arg: DefaultSourceArg, Identifier: DefaultSourceID,
	}}
}

// LoadSources reads sources.json, falling back to the defaults.
func LoadSources() ([]SourceConfig, error) {
	b, err := os.ReadFile(sourcesFile())
	if errors.Is(err, os.ErrNotExist) {
		return DefaultSources(), nil
	}
	if err != nil {
		return nil, err
	}
	var s []SourceConfig
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", sourcesFile(), err)
	}
	return s, nil
}

// SaveSources writes sources.json.
func SaveSources(s []SourceConfig) error {
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(sourcesFile(), b, 0o644)
}

// ResetSources removes sources.json so defaults apply again.
func ResetSources() error {
	err := os.Remove(sourcesFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

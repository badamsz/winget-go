package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvFileName is the settings file winget-go looks for automatically.
const EnvFileName = "winget-go.env"

// listKeys accumulate across every env file and the real environment
// instead of the first value winning, so a machine-wide exclusion list and
// a user's own list combine.
var listKeys = map[string]bool{
	"WINGET_GO_EXCLUDE":      true,
	"WINGET_GO_EXCLUDE_FILE": true,
}

// fileLists holds list-key values read from env files.
var fileLists = map[string][]string{}

// allowedKey limits env files to winget-go's own settings (plus GitHub
// tokens), so a dropped-in file cannot alter PATH or other process state.
func allowedKey(k string) bool {
	return strings.HasPrefix(k, "WINGET_GO_") || k == "GITHUB_TOKEN" || k == "GH_TOKEN"
}

// EnvFileCandidates lists env files in precedence order (highest first).
// WINGET_GO_ENV_FILE, when set, replaces the search entirely.
func EnvFileCandidates() []string {
	if f := os.Getenv("WINGET_GO_ENV_FILE"); f != "" {
		return []string{f}
	}
	c := []string{filepath.Join(ConfigDir(), EnvFileName)}
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			c = append(c, filepath.Join(pd, "winget-go", EnvFileName))
		}
	} else {
		c = append(c, filepath.Join("/etc", "winget-go", EnvFileName))
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		c = append(c, filepath.Join(filepath.Dir(exe), EnvFileName))
	}
	return c
}

// LoadEnvFiles applies every existing env file. Real environment
// variables always win for scalar settings; list settings
// (WINGET_GO_EXCLUDE, WINGET_GO_EXCLUDE_FILE) are merged. It returns the
// files that were loaded.
func LoadEnvFiles() ([]string, error) {
	explicit := os.Getenv("WINGET_GO_ENV_FILE") != ""
	var loaded []string
	seen := map[string]bool{}
	for _, f := range EnvFileCandidates() {
		abs, _ := filepath.Abs(f)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		vals, err := ParseEnvFile(f)
		if errors.Is(err, os.ErrNotExist) && !explicit {
			continue
		}
		if err != nil {
			return loaded, err
		}
		loaded = append(loaded, f)
		for _, kv := range vals {
			switch {
			case !allowedKey(kv[0]):
				fmt.Fprintf(os.Stderr, "warning: %s: ignoring %s (only WINGET_GO_*, GITHUB_TOKEN and GH_TOKEN are allowed)\n", f, kv[0])
			case listKeys[kv[0]]:
				fileLists[kv[0]] = append(fileLists[kv[0]], kv[1])
			default:
				if _, set := os.LookupEnv(kv[0]); !set {
					os.Setenv(kv[0], kv[1])
				}
			}
		}
	}
	return loaded, nil
}

// ListSetting returns all values of a list key: the real environment
// first, then every env file.
func ListSetting(key string) []string {
	var out []string
	if v := os.Getenv(key); v != "" {
		out = append(out, v)
	}
	return append(out, fileLists[key]...)
}

// ParseEnvFile reads a dotenv-style file: KEY=VALUE lines, optional
// "export " prefix, # comments, single/double quotes. Double-quoted values
// may span lines and support \n, \t, \" and \\ escapes.
func ParseEnvFile(path string) ([][2]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out [][2]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNo)
		}
		v = strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(v, `"`):
			body := v[1:]
			for !closesDouble(body) {
				if !sc.Scan() {
					return nil, fmt.Errorf("%s:%d: unterminated quoted value for %s", path, lineNo, k)
				}
				lineNo++
				body += "\n" + sc.Text()
			}
			end := closingIndex(body)
			v = unescape(body[:end])
		case strings.HasPrefix(v, "'"):
			end := strings.Index(v[1:], "'")
			if end < 0 {
				return nil, fmt.Errorf("%s:%d: unterminated quoted value for %s", path, lineNo, k)
			}
			v = v[1 : end+1]
		default:
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		out = append(out, [2]string{k, v})
	}
	return out, sc.Err()
}

func closingIndex(s string) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

func closesDouble(s string) bool { return closingIndex(s) >= 0 }

func unescape(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`)
	return r.Replace(s)
}

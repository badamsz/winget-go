package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.env")
	write(t, p, `# comment
export WINGET_GO_BACKEND=portable   # trailing comment
WINGET_GO_GITHUB='1 # not a comment'
WINGET_GO_EXCLUDE="
  Microsoft.Edge*
  # Google.Chrome
  name:Visual \"Studio\"*
"
EMPTY=
`)
	got, err := ParseEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{
		{"WINGET_GO_BACKEND", "portable"},
		{"WINGET_GO_GITHUB", "1 # not a comment"},
		{"WINGET_GO_EXCLUDE", "\n  Microsoft.Edge*\n  # Google.Chrome\n  name:Visual \"Studio\"*\n"},
		{"EMPTY", ""},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestParseEnvFileErrors(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"noeq":      "JUSTAKEY\n",
		"unterm":    "A=\"abc\n",
		"unterm1":   "A='abc\n",
		"spacedkey": "A B=1\n",
	} {
		p := filepath.Join(dir, name)
		write(t, p, body)
		if _, err := ParseEnvFile(p); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadEnvFilesPrecedenceAndMerge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WINGET_GO_HOME", home)
	t.Setenv("WINGET_GO_ENV_FILE", "")
	t.Setenv("WINGET_GO_EXCLUDE", "Env.Pkg")
	t.Setenv("WINGET_GO_BACKEND", "native") // real env must win
	os.Unsetenv("WINGET_GO_GITHUB")
	t.Cleanup(func() { os.Unsetenv("WINGET_GO_GITHUB"); fileLists = map[string][]string{} })
	fileLists = map[string][]string{}

	write(t, filepath.Join(ConfigDir(), EnvFileName), "WINGET_GO_BACKEND=portable\nWINGET_GO_GITHUB=1\nWINGET_GO_EXCLUDE=User.*\nPATH=/evil\n")
	loaded, err := LoadEnvFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) == 0 {
		t.Fatal("user env file not loaded")
	}
	if os.Getenv("WINGET_GO_BACKEND") != "native" {
		t.Error("env file overrode a real environment variable")
	}
	if os.Getenv("WINGET_GO_GITHUB") != "1" {
		t.Error("env file value not applied")
	}
	if os.Getenv("PATH") == "/evil" {
		t.Error("disallowed key was applied")
	}
	if got := ListSetting("WINGET_GO_EXCLUDE"); !slices.Equal(got, []string{"Env.Pkg", "User.*"}) {
		t.Errorf("merged excludes = %q", got)
	}
}

func TestExplicitEnvFileMustExist(t *testing.T) {
	t.Setenv("WINGET_GO_ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	if _, err := LoadEnvFiles(); err == nil {
		t.Fatal("expected error for missing WINGET_GO_ENV_FILE")
	}
}

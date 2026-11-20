package exclude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"winget-go/internal/config"
)

func TestSplit(t *testing.T) {
	got := Split("Microsoft.Edge*, Google.Chrome;\n  # whole-line comment\nMozilla.Firefox # trailing\n\n")
	want := []string{"Microsoft.Edge*", "Google.Chrome", "Mozilla.Firefox"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestMatch(t *testing.T) {
	l := &List{}
	if err := l.Add("test", "microsoft.edge*", "Git.Git", "name:Visual Studio*"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id, name string
		want     bool
	}{
		{"Microsoft.Edge", "Microsoft Edge", true},
		{"Microsoft.EdgeWebView2Runtime", "WebView2", true},
		{"git.git", "Git", true},
		{"Git.MinGit", "MinGit", false},
		{"Microsoft.VisualStudio.2022.Community", "Visual Studio Community 2022", true},
		{"Microsoft.VisualStudioCode", "Microsoft Visual Studio Code", false},
	}
	for _, c := range cases {
		if _, got := l.Match(c.id, c.name); got != c.want {
			t.Errorf("Match(%s, %s) = %v", c.id, c.name, got)
		}
	}
	var nilList *List
	if _, ok := nilList.Match("x", "y"); ok {
		t.Error("nil list must not exclude")
	}
	if err := l.Add("test", "bad["); err == nil {
		t.Error("expected invalid pattern error")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "ex.txt")
	os.WriteFile(file, []byte("# list\nFile.One\nFile.Two*\n"), 0o644)
	t.Setenv("WINGET_GO_EXCLUDE", "Env.Pkg")
	t.Setenv("WINGET_GO_EXCLUDE_FILE", file)

	l, err := Load(Options{Patterns: []string{"Flag.A,Flag.B"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"Env.Pkg", "File.One", "File.TwoX", "Flag.A", "Flag.B"} {
		if _, ok := l.Match(id, ""); !ok {
			t.Errorf("%s should be excluded", id)
		}
	}
	if !Configured() {
		t.Error("Configured() should be true")
	}
	l, _ = Load(Options{Disable: true})
	if l.Len() != 0 {
		t.Error("--no-exclude must clear the list")
	}
	if _, err := Load(Options{Files: []string{filepath.Join(dir, "missing")}}); err == nil {
		t.Error("expected error for missing exclusion file")
	}
}

// The shipped example must stay parseable and its patterns valid.
func TestExampleEnvFile(t *testing.T) {
	vals, err := config.ParseEnvFile("../../winget-go.env.example")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, kv := range vals {
		if kv[0] != "WINGET_GO_EXCLUDE" {
			continue
		}
		found = true
		l := &List{}
		if err := l.Add("example", Split(kv[1])...); err != nil {
			t.Fatal(err)
		}
		if _, ok := l.Match("Microsoft.EdgeWebView2Runtime", ""); !ok || l.Len() < 3 {
			t.Fatalf("unexpected example patterns: %v", l.Patterns())
		}
	}
	if !found {
		t.Fatal("example does not set WINGET_GO_EXCLUDE")
	}
}

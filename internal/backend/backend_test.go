package backend

import (
	"slices"
	"testing"
)

func TestDecide(t *testing.T) {
	t.Setenv("WINGET_GO_BACKEND", "")
	found := func() string { return `C:\winget.exe` }
	none := func() string { return "" }

	d, err := Decide([]string{"install", "7zip.7zip", "--backend", "portable"}, found, Hints{})
	if err != nil || d.Kind != Portable || !slices.Equal(d.Args, []string{"install", "7zip.7zip"}) {
		t.Fatalf("explicit portable: %+v %v", d, err)
	}
	if d, _ := Decide([]string{"search", "x"}, found, Hints{}); d.Kind != Native || d.NativePath != `C:\winget.exe` {
		t.Fatalf("auto with native: %+v", d)
	}
	if d, _ := Decide([]string{"search", "x"}, none, Hints{}); d.Kind != Portable {
		t.Fatalf("auto without native: %+v", d)
	}
	if d, _ := Decide([]string{"install", "x", "--github"}, found, Hints{}); d.Kind != Portable {
		t.Fatalf("--github should force portable: %+v", d)
	}
	if _, err := Decide([]string{"--backend=native"}, none, Hints{}); err == nil {
		t.Fatal("expected error for missing native winget")
	}
	if _, err := Decide([]string{"--backend=bogus"}, found, Hints{}); err == nil {
		t.Fatal("expected error for unknown backend")
	}
	t.Setenv("WINGET_GO_BACKEND", "portable")
	if d, _ := Decide([]string{"list"}, found, Hints{}); d.Kind != Portable {
		t.Fatalf("env portable: %+v", d)
	}
	// Arguments after -- are passed through untouched.
	if d, _ := Decide([]string{"install", "--", "--backend", "x"}, found, Hints{}); !slices.Equal(d.Args, []string{"install", "--", "--backend", "x"}) {
		t.Fatalf("-- handling: %+v", d.Args)
	}

	t.Setenv("WINGET_GO_BACKEND", "")
	ex := Hints{ExclusionsConfigured: true}
	if d, _ := Decide([]string{"upgrade", "--all"}, found, ex); d.Kind != Portable {
		t.Fatalf("exclusions should force portable for upgrade: %+v", d)
	}
	if d, _ := Decide([]string{"list"}, found, ex); d.Kind != Native {
		t.Fatalf("plain list should stay native: %+v", d)
	}
	if d, _ := Decide([]string{"list", "--upgrade-available"}, found, ex); d.Kind != Portable {
		t.Fatalf("list --upgrade-available should be portable: %+v", d)
	}
	if d, _ := Decide([]string{"search", "x"}, found, ex); d.Kind != Native {
		t.Fatalf("exclusions must not affect search: %+v", d)
	}
	if d, _ := Decide([]string{"--backend", "native", "upgrade"}, found, ex); d.Kind != Native || d.Warning == "" {
		t.Fatalf("explicit native should warn: %+v", d)
	}
	if d, _ := Decide([]string{"upgrade", "--no-exclude"}, found, Hints{}); d.Kind != Portable {
		t.Fatalf("--no-exclude is a winget-go flag: %+v", d)
	}
	if d, _ := Decide([]string{"uninstall", "--old-versions=minor"}, found, Hints{}); d.Kind != Portable {
		t.Fatalf("--old-versions is a winget-go flag: %+v", d)
	}
	if d, _ := Decide([]string{"upgrade", "--all", "--migrate"}, found, Hints{}); d.Kind != Portable {
		t.Fatalf("--migrate is a winget-go flag: %+v", d)
	}
	for _, args := range [][]string{
		{"uninstall", "Microsoft.DotNet.*"},
		{"rm", "--id", "Python.Python.3.1?"},
		{"uninstall", "--name=Python 3.*"},
		{"upgrade", "Microsoft.VCRedist.*"},
		{"update", "--id", "Microsoft.DotNet.?"},
	} {
		if d, _ := Decide(args, found, Hints{}); d.Kind != Portable {
			t.Fatalf("wildcard uninstall %v should be portable: %+v", args, d)
		}
	}
	if d, _ := Decide([]string{"uninstall", "7zip.7zip"}, found, Hints{}); d.Kind != Native {
		t.Fatalf("plain uninstall should stay native: %+v", d)
	}
	if d, _ := Decide([]string{"search", "foo*"}, found, Hints{}); d.Kind != Native {
		t.Fatalf("wildcards only matter for uninstall/upgrade: %+v", d)
	}
	if d, _ := Decide([]string{"--backend=native", "uninstall", "foo*"}, found, Hints{}); d.Kind != Native || d.Warning == "" {
		t.Fatalf("explicit native wildcard uninstall should warn: %+v", d)
	}
}

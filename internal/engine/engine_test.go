package engine

import (
	"slices"
	"testing"

	"winget-go/internal/installed"
)

func TestMatchInstalledWildcard(t *testing.T) {
	items := []*installed.Item{
		pkg("Microsoft.DotNet.Runtime.8", arp("Microsoft .NET Runtime - 8.0.11 (x64)", "8.0.11", "Microsoft Corporation", "machine")),
		pkg("Microsoft.DotNet.SDK.8", arp("Microsoft .NET SDK 8.0.404 (x64)", "8.0.404", "Microsoft Corporation", "machine")),
		arp("Python 3.11.9 (64-bit)", "3.11.9", "PSF", "machine"),
		arp("Python 3.12.1 (64-bit)", "3.12.1", "PSF", "machine"),
		arp("Python Launcher", "3.12.1", "PSF", "machine"),
	}
	cases := map[string][]string{
		"microsoft.dotnet.*":     {"8.0.11", "8.0.404"},
		"microsoft.dotnet.sdk.?": {"8.0.404"},
		"python 3.1[12].* (*)":   {"3.11.9", "3.12.1"},
		"python *":               {"3.11.9", "3.12.1", "3.12.1"},
		"python":                 nil, // glob must match the whole name
		"*launcher":              {"3.12.1"},
		`arp\hklm\python 3.12*`:  {"3.12.1"}, // backslashes are literal
	}
	for pattern, want := range cases {
		got := versions(matchInstalled(items, globPattern(pattern), false, true))
		if !slices.Equal(got, want) {
			t.Errorf("%q matched %v, want %v", pattern, got, want)
		}
	}
	// Without wildcards the old substring behaviour applies.
	if got := matchInstalled(items, "python", false, false); len(got) != 3 {
		t.Errorf("substring match = %d items", len(got))
	}
}

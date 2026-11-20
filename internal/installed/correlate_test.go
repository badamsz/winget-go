package installed

import (
	"testing"

	"winget-go/internal/platform"
)

func TestItemLabel(t *testing.T) {
	cases := []struct {
		it   Item
		want string
	}{
		{Item{Name: "WindowsAppRuntime.1.8", Version: "8000.994.2142.0", MSIX: &platform.MSIXPackage{Architecture: "x86"}}, "WindowsAppRuntime.1.8 8000.994.2142.0 (x86)"},
		{Item{Name: "Notepad++", Version: "1.0.0.0", MSIX: &platform.MSIXPackage{Architecture: "neutral"}}, "Notepad++ 1.0.0.0"},
		{Item{Name: "Python 3.12.1 (64-bit)", Version: "3.12.1"}, "Python 3.12.1 (64-bit)"}, // version already in name
		{Item{Name: "Tool"}, "Tool"},
	}
	for _, c := range cases {
		if got := c.it.Label(); got != c.want {
			t.Errorf("Label() = %q want %q", got, c.want)
		}
	}
}

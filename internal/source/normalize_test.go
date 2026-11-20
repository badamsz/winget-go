package source

import (
	"slices"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"7-Zip 26.06 (x64)":                   "7zip(X64)",
		"Mozilla Firefox (x64 en-US)":         "mozillafirefox(X64)",
		"Mozilla Firefox 120.0.1 (x64 en-US)": "mozillafirefox",
		"Git":                                 "git",
		"Microsoft Visual Studio Code":        "microsoftvisualstudiocode",
		"PowerToys (Preview) x64":             "powertoys(X64)",
	}
	for in, want := range cases {
		if got := NormalizeName(in); !slices.Contains(got, want) {
			t.Errorf("NormalizeName(%q) = %v, want to contain %q", in, got, want)
		}
	}
}

func TestNormalizePublisher(t *testing.T) {
	cases := map[string]string{
		"Microsoft Corporation": "microsoft",
		"Mozilla":               "mozilla",
		"Igor Pavlov":           "igorpavlov",
		"Docker Inc.":           "docker",
	}
	for in, want := range cases {
		if got := NormalizePublisher(in); got != want {
			t.Errorf("NormalizePublisher(%q) = %q, want %q", in, got, want)
		}
	}
}

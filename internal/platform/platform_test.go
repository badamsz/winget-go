package platform

import "testing"

func TestParseAppxJSON(t *testing.T) {
	in := "\uFEFF" + `[{"Name":"Microsoft.WindowsAppRuntime.1.8","FullName":"Microsoft.WindowsAppRuntime.1.8_8000.675.1142.0_x64__8wekyb3d8bbwe",
	"FamilyName":"Microsoft.WindowsAppRuntime.1.8_8wekyb3d8bbwe","Version":"8000.675.1142.0","Architecture":"X64",
	"Publisher":"CN=Microsoft Corporation, O=Microsoft Corporation, L=Redmond, S=Washington, C=US","IsFramework":true}]`
	pkgs, err := ParseAppxJSON([]byte(in))
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("%v %+v", err, pkgs)
	}
	p := pkgs[0]
	if p.Architecture != "x64" || p.DisplayName != p.Name || p.PublisherDisplayName != "Microsoft Corporation" || !p.IsFramework {
		t.Fatalf("%+v", p)
	}
	// PowerShell can unwrap a one-element array.
	if pkgs, err := ParseAppxJSON([]byte(`{"Name":"A","Version":"1.0"}`)); err != nil || len(pkgs) != 1 {
		t.Fatalf("single object: %v %+v", err, pkgs)
	}
	if pkgs, err := ParseAppxJSON([]byte("  \r\n")); err != nil || pkgs != nil {
		t.Fatalf("empty: %v %+v", err, pkgs)
	}
}

func TestSubjectName(t *testing.T) {
	for in, want := range map[string]string{
		"CN=Microsoft Corporation, O=Microsoft Corporation, C=US": "Microsoft Corporation",
		`O="Contoso, Ltd", C=US`:                                  "Contoso, Ltd",
		"CN=33823Nicke":                                           "33823Nicke",
		"weird":                                                   "weird",
	} {
		if got := SubjectName(in); got != want {
			t.Errorf("SubjectName(%q) = %q want %q", in, got, want)
		}
	}
}

func TestMSResourceURI(t *testing.T) {
	for in, want := range map[string]string{
		"ms-resource:AppName":                 "ms-resource://Pkg/Resources/AppName",
		"ms-resource:Strings/AppName":         "ms-resource://Pkg/Strings/AppName",
		"ms-resource:/Resources/AppName":      "ms-resource://Pkg/Resources/AppName",
		"ms-resource://Other/Resources/Title": "ms-resource://Other/Resources/Title",
		"Plain Name":                          "",
	} {
		if got := MSResourceURI("Pkg", in); got != want {
			t.Errorf("MSResourceURI(%q) = %q want %q", in, got, want)
		}
	}
}

func TestLegacyARPKey(t *testing.T) {
	for in, want := range map[string]string{
		`HKLM\{GUID}`:        `Machine\X64\{GUID}`,
		`hklm32\7-Zip`:       `Machine\X86\7-Zip`,
		`HKCU\Foo`:           `User\X64\Foo`,
		`Machine\X64\{GUID}`: `Machine\X64\{GUID}`,
		`HKLMX`:              `HKLMX`,
	} {
		if got := LegacyARPKey(in); got != want {
			t.Errorf("LegacyARPKey(%q) = %q want %q", in, got, want)
		}
	}
}

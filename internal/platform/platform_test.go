package platform

import (
	"slices"
	"strings"
	"testing"
)

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

// clixmlSample is real Remove-AppxPackage stderr (trimmed), including the
// progress records and _x000D__x000A_ escapes PowerShell emits.
const clixmlSample = `#< CLIXML
<Objs Version="1.1.0.1" xmlns="http://schemas.microsoft.com/powershell/2004/04"><Obj S="progress" RefId="0"><TN RefId="0"><T>System.Management.Automation.PSCustomObject</T><T>System.Object</T></TN><MS><I64 N="SourceId">1</I64><PR N="Record"><AV>Preparing modules for first use.</AV><AI>0</AI><Nil /><PI>-1</PI><PC>-1</PC><T>Completed</T><SR>-1</SR><SD> </SD></PR></MS></Obj><S S="Error">Remove-AppxPackage : Deployment failed with HRESULT: 0x80073CF3, Package failed updates, dependency or conflict validation._x000D__x000A_</S><S S="Error">Windows cannot remove framework Microsoft.WindowsAppRuntime.1.8_8000.994.2142.0_x005F_x64__8wekyb3d8bbwe because package(s) MicrosoftCorporationII.WinAppRuntime.Main.1.8 Microsoft.DesktopAppInstaller _x000D__x000A_</S><S S="Error">Microsoft.BingSearch currently depends on the framework. Removing all packages that depend on the framework automatically removes the framework._x000D__x000A_</S><S S="Error">NOTE: For additional information, look for [ActivityId] 7ac9901f-7916-0006-146f-ce7a1679dd01 in the Event Log or use the command line Get-AppPackageLog -ActivityID 7ac9901f-7916-0006-146f-ce7a1679dd01_x000D__x000A_</S><S S="Error">At line:2 char:1_x000D__x000A_</S><S S="Error">    + FullyQualifiedErrorId : DeploymentError,Microsoft.Windows.Appx.PackageManager.Commands.RemoveAppxPackageCommand_x000D__x000A_</S></Objs>`

func TestDecodeCLIXML(t *testing.T) {
	got := DecodeCLIXML(clixmlSample)
	if strings.Contains(got, "<") || strings.Contains(got, "_x000D_") || strings.Contains(got, "Preparing modules") {
		t.Fatalf("not decoded: %q", got)
	}
	if !strings.Contains(got, "WindowsAppRuntime.1.8_8000.994.2142.0_x64__8wekyb3d8bbwe") {
		t.Fatalf("_x005F_ not unescaped: %q", got)
	}
	if DecodeCLIXML("  plain error \n") != "plain error" {
		t.Fatal("plain text should pass through")
	}
}

func TestParseMSIXErrorFrameworkInUse(t *testing.T) {
	e := ParseMSIXError(clixmlSample, 0)
	if e.HResult != HResultDependencyFailure {
		t.Fatalf("hresult = %08X", e.HResult)
	}
	want := []string{"MicrosoftCorporationII.WinAppRuntime.Main.1.8", "Microsoft.DesktopAppInstaller", "Microsoft.BingSearch"}
	if !slices.Equal(e.UsedBy, want) {
		t.Fatalf("UsedBy = %q", e.UsedBy)
	}
	msg := e.Error()
	if !strings.Contains(msg, "in use by MicrosoftCorporationII.WinAppRuntime.Main.1.8, Microsoft.DesktopAppInstaller, Microsoft.BingSearch") ||
		strings.Contains(msg, "ActivityId") || strings.Contains(msg, "\n") {
		t.Fatalf("message: %q", msg)
	}
}

func TestParseMSIXErrorPlain(t *testing.T) {
	// What our catch block prints: the HRESULT is passed separately.
	e := ParseMSIXError("Deployment failed with HRESULT: 0x80073CFA, Removal failed.\r\nNOTE: For additional information, look for [ActivityId] x", 0x80073CFA)
	if e.Error() != "Deployment failed with HRESULT: 0x80073CFA, Removal failed." {
		t.Fatalf("message: %q", e.Error())
	}
	e = ParseMSIXError("Something odd happened.", 0x80070002)
	if e.Error() != "Something odd happened. (0x80070002)" {
		t.Fatalf("message: %q", e.Error())
	}
	if e := ParseMSIXError("Access is denied.", HResultAccessDenied); !strings.Contains(e.Error(), "elevated") {
		t.Fatalf("message: %q", e.Error())
	}
}

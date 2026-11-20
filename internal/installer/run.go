package installer

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"winget-go/internal/config"
	"winget-go/internal/manifest"
	"winget-go/internal/platform"
)

// ExecOptions controls installer execution.
type ExecOptions struct {
	Args      ArgOptions
	PackageID string
	Commands  []string // manifest Commands (portable alias fallback)
}

// Result reports what an execution did.
type Result struct {
	ExitCode        int64
	RebootRequired  bool
	InstallLocation string   // portable packages
	Links           []string // portable packages
	PathChanged     bool
}

// NeedsElevation decides whether to launch the installer through UAC.
func NeedsElevation(in *manifest.Installer, scope string) bool {
	if platform.IsElevated() {
		return false
	}
	switch strings.ToLower(in.ElevationRequirement) {
	case "elevationrequired":
		return true
	case "elevationprohibited", "elevatesself":
		return false
	}
	t := effectiveType(in)
	if scope == "" {
		scope = in.Scope
	}
	return (t == "msi" || t == "wix") && strings.EqualFold(scope, "machine")
}

// Execute runs a downloaded installer file.
func Execute(ctx context.Context, in *manifest.Installer, file string, o ExecOptions) (*Result, error) {
	if !platform.IsWindows() {
		return nil, platform.ErrUnsupported
	}
	switch strings.ToLower(in.InstallerType) {
	case "portable":
		return installPortable(in, file, "", o)
	case "zip":
		return executeZip(ctx, in, file, o)
	}
	return runFile(ctx, in, file, o)
}

func runFile(ctx context.Context, in *manifest.Installer, file string, o ExecOptions) (*Result, error) {
	scope := o.Args.Scope
	elevate := NeedsElevation(in, scope)
	args := BuildArgs(in, o.Args)
	var code int64
	var err error
	switch effectiveType(in) {
	case "msi", "wix":
		code, err = platform.Run(ctx, systemExe("msiexec.exe"), fmt.Sprintf(`/i "%s" %s`, file, args), elevate)
	case "msix", "appx":
		ps := fmt.Sprintf(`Add-AppxPackage -Path '%s'`, strings.ReplaceAll(file, "'", "''"))
		if strings.EqualFold(scope, "machine") {
			ps = fmt.Sprintf(`Add-AppxProvisionedPackage -Online -SkipLicense -PackagePath '%s'`, strings.ReplaceAll(file, "'", "''"))
			elevate = !platform.IsElevated()
		}
		code, err = platform.Run(ctx, systemExe(`WindowsPowerShell\v1.0\powershell.exe`),
			`-NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "`+ps+`"`, elevate)
	case "msstore":
		return nil, fmt.Errorf("Microsoft Store packages are not supported")
	default: // exe, inno, nullsoft, burn
		code, err = platform.Run(ctx, file, args, elevate)
	}
	if err != nil {
		return nil, err
	}
	ok, reboot := IsSuccess(in, code)
	if !ok {
		msg := fmt.Sprintf("installer failed with exit code %d (0x%08X)", code, uint32(code))
		if d := DescribeExitCode(in, code); d != "" {
			msg += ": " + d
		}
		return &Result{ExitCode: code}, fmt.Errorf("%s", msg)
	}
	return &Result{ExitCode: code, RebootRequired: reboot}, nil
}

func systemExe(rel string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", rel)
}

func executeZip(ctx context.Context, in *manifest.Installer, file string, o ExecOptions) (*Result, error) {
	if err := os.MkdirAll(config.DataDir(), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(config.DataDir(), "extract-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := Unzip(file, tmp); err != nil {
		return nil, err
	}
	if strings.EqualFold(in.NestedInstallerType, "portable") {
		return installPortable(in, "", tmp, o)
	}
	if len(in.NestedInstallerFiles) == 0 {
		return nil, fmt.Errorf("zip installer has no NestedInstallerFiles")
	}
	nested, err := safeJoin(tmp, in.NestedInstallerFiles[0].RelativeFilePath)
	if err != nil {
		return nil, err
	}
	return runFile(ctx, in, nested, o)
}

// Unzip extracts an archive, rejecting entries that escape dst.
func Unzip(file, dst string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		p, err := safeJoin(dst, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func safeJoin(root, rel string) (string, error) {
	rel = filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/"))
	p := filepath.Join(root, rel)
	r, err := filepath.Rel(root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive path %q escapes the extraction directory", rel)
	}
	return p, nil
}

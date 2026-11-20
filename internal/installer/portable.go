package installer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"winget-go/internal/config"
	"winget-go/internal/manifest"
	"winget-go/internal/platform"
)

var unsafeID = regexp.MustCompile(`[^A-Za-z0-9._+-]`)

// PackagesDir is where portable packages are installed.
func PackagesDir() string { return filepath.Join(config.DataDir(), "Packages") }

// LinksDir holds command links for portable packages; it is added to PATH.
func LinksDir() string { return filepath.Join(config.DataDir(), "Links") }

// installPortable installs either a single portable exe (file) or an
// extracted archive (dir) into PackagesDir and creates command links.
func installPortable(in *manifest.Installer, file, dir string, o ExecOptions) (*Result, error) {
	target := o.Args.InstallLocation
	if target == "" {
		target = filepath.Join(PackagesDir(), unsafeID.ReplaceAllString(o.PackageID, "_"))
	}
	if err := os.RemoveAll(target); err != nil {
		return nil, fmt.Errorf("remove previous version: %w", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, err
	}
	type cmd struct{ alias, path string }
	var cmds []cmd
	if dir != "" {
		if err := copyTree(dir, target); err != nil {
			return nil, err
		}
		for _, nf := range in.NestedInstallerFiles {
			p, err := safeJoin(target, nf.RelativeFilePath)
			if err != nil {
				return nil, err
			}
			alias := nf.PortableCommandAlias
			if alias == "" {
				alias = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			}
			cmds = append(cmds, cmd{alias, p})
		}
	} else {
		alias := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if len(o.Commands) > 0 {
			alias = o.Commands[0]
		}
		dst := filepath.Join(target, alias+".exe")
		if err := copyFile(file, dst); err != nil {
			return nil, err
		}
		cmds = append(cmds, cmd{alias, dst})
	}

	res := &Result{InstallLocation: target}
	pathDir := LinksDir()
	if in.ArchiveBinariesDependOnPath {
		pathDir = target
	} else {
		if err := os.MkdirAll(LinksDir(), 0o755); err != nil {
			return nil, err
		}
		for _, c := range cmds {
			link, err := makeLink(c.alias, c.path)
			if err != nil {
				return nil, err
			}
			res.Links = append(res.Links, link)
		}
	}
	changed, err := platform.AddToUserPath(pathDir)
	if err != nil {
		return nil, fmt.Errorf("add %s to PATH: %w", pathDir, err)
	}
	res.PathChanged = changed
	return res, nil
}

// makeLink creates <LinksDir>\<alias>.exe as a symlink, falling back to a
// .cmd shim when symlinks are not permitted (no Developer Mode / admin).
func makeLink(alias, target string) (string, error) {
	link := filepath.Join(LinksDir(), alias+".exe")
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err == nil {
		return link, nil
	}
	shim := filepath.Join(LinksDir(), alias+".cmd")
	body := fmt.Sprintf("@\"%s\" %%*\r\n", target)
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		return "", err
	}
	return shim, nil
}

// RemovePortable deletes a portable package and its links.
func RemovePortable(location string, links []string) error {
	for _, l := range links {
		if err := os.Remove(l); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if location == "" {
		return nil
	}
	// Only delete directories we manage, never arbitrary --location paths'
	// parents.
	return os.RemoveAll(location)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		return copyFile(p, t)
	})
}

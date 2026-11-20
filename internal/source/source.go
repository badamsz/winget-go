// Package source implements the winget "Microsoft.PreIndexed.Package"
// source (schema v2): a signed MSIX containing a SQLite index, plus
// per-package version data and merged manifests on a CDN.
package source

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"

	"winget-go/internal/config"
	"winget-go/internal/httpx"
	"winget-go/internal/manifest"
	"winget-go/internal/mszyml"
	"winget-go/internal/version"
)

const indexPackage = "source2.msix"

// Source is an opened pre-indexed source.
type Source struct {
	Config config.SourceConfig
	dir    string
	db     *sql.DB
}

// Package is one row of the index.
type Package struct {
	Source        *Source
	RowID         int64
	ID            string
	Name          string
	Moniker       string
	LatestVersion string
	ARPMin        string
	ARPMax        string
	Hash          []byte
}

// VersionEntry is one entry of a package's version data.
type VersionEntry struct {
	Version      string `yaml:"v"`
	RelativePath string `yaml:"rP"`
	SHA256       string `yaml:"s256H"`
}

type versionData struct {
	SchemaVersion string         `yaml:"sV"`
	Versions      []VersionEntry `yaml:"vD"`
}

type meta struct {
	ETag         string    `json:"etag"`
	LastModified string    `json:"lastModified"`
	LastCheck    time.Time `json:"lastCheck"`
}

// Dir returns the cache directory for a source.
func Dir(c config.SourceConfig) string {
	return filepath.Join(config.CacheDir(), "sources", safeName(c.Name))
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._+-]`)

func safeName(s string) string { return unsafeChars.ReplaceAllString(s, "_") }

// Open opens a source, refreshing its index if it is missing or older
// than the auto-update interval. A failed refresh is reported through
// warn but is not fatal when a previous index exists.
func Open(ctx context.Context, c config.SourceConfig, warn io.Writer) (*Source, error) {
	if c.Type != "" && c.Type != config.PreIndexedType {
		return nil, fmt.Errorf("source %q: type %q is not supported", c.Name, c.Type)
	}
	s := &Source{Config: c, dir: Dir(c)}
	m := s.readMeta()
	_, statErr := os.Stat(s.indexPath())
	interval := config.AutoUpdateInterval()
	if statErr != nil || (interval > 0 && time.Since(m.LastCheck) > interval) {
		if err := s.Update(ctx, false); err != nil {
			if statErr != nil {
				return nil, err
			}
			fmt.Fprintf(warn, "warning: failed to update source %q, using cached index: %v\n", c.Name, err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(s.indexPath())+"?mode=ro")
	if err != nil {
		return nil, err
	}
	s.db = db
	return s, nil
}

// Close releases the index.
func (s *Source) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Source) indexPath() string { return filepath.Join(s.dir, "index.db") }
func (s *Source) metaPath() string  { return filepath.Join(s.dir, "meta.json") }

func (s *Source) readMeta() meta {
	var m meta
	if b, err := os.ReadFile(s.metaPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *Source) writeMeta(m meta) error {
	b, _ := json.Marshal(m)
	return os.WriteFile(s.metaPath(), b, 0o644)
}

// Update downloads the source package (conditionally, unless force) and
// extracts its index.
func (s *Source) Update(ctx context.Context, force bool) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	m := s.readMeta()
	url := strings.TrimRight(s.Config.Arg, "/") + "/" + indexPackage
	req, err := httpx.NewRequest(ctx, url)
	if err != nil {
		return err
	}
	if _, err := os.Stat(s.indexPath()); err == nil && !force {
		if m.ETag != "" {
			req.Header.Set("If-None-Match", m.ETag)
		}
		if m.LastModified != "" {
			req.Header.Set("If-Modified-Since", m.LastModified)
		}
	}
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		m.LastCheck = time.Now()
		return s.writeMeta(m)
	case http.StatusOK:
	default:
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(s.dir, "source-*.msix")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if err := extractIndex(tmp.Name(), s.indexPath()); err != nil {
		return err
	}
	return s.writeMeta(meta{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		LastCheck:    time.Now(),
	})
}

func extractIndex(msix, dst string) error {
	zr, err := zip.OpenReader(msix)
	if err != nil {
		return fmt.Errorf("open source package: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "Public/index.db" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		tmp := dst + ".tmp"
		out, err := os.Create(tmp)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if err := validateIndex(tmp); err != nil {
			os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, dst)
	}
	return errors.New("source package does not contain Public/index.db")
}

func validateIndex(path string) error {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var major string
	if err := db.QueryRow(`SELECT value FROM metadata WHERE name='majorVersion'`).Scan(&major); err != nil {
		return fmt.Errorf("index is not a winget source index: %w", err)
	}
	if major != "2" {
		return fmt.Errorf("unsupported index schema version %s (need 2)", major)
	}
	return nil
}

const pkgCols = `p.rowid, p.id, p.name, IFNULL(p.moniker,''), p.latest_version, IFNULL(p.arp_min_version,''), IFNULL(p.arp_max_version,''), p.hash`

func (s *Source) scan(rows *sql.Rows, extra ...any) (*Package, error) {
	p := &Package{Source: s}
	dest := append([]any{&p.RowID, &p.ID, &p.Name, &p.Moniker, &p.LatestVersion, &p.ARPMin, &p.ARPMax, &p.Hash}, extra...)
	return p, rows.Scan(dest...)
}

// Lookup returns the package with exactly this id (case-insensitive).
func (s *Source) Lookup(id string) (*Package, error) {
	rows, err := s.db.Query(`SELECT `+pkgCols+` FROM packages p WHERE p.id = ? COLLATE NOCASE`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if rows.Next() {
		return s.scan(rows)
	}
	return nil, rows.Err()
}

// ByProductCode finds packages that declare an ARP product code.
func (s *Source) ByProductCode(code string) ([]*Package, error) {
	return s.collect(`SELECT `+pkgCols+` FROM productcodes2 c JOIN packages p ON p.rowid = c.package WHERE c.productcode = ?`,
		strings.ToLower(code))
}

// ByFamilyName finds packages that declare an MSIX package family name.
func (s *Source) ByFamilyName(pfn string) ([]*Package, error) {
	return s.collect(`SELECT `+pkgCols+` FROM pfns2 f JOIN packages p ON p.rowid = f.package WHERE f.pfn = ?`,
		strings.ToLower(pfn))
}

// ByNormalizedName finds packages by normalized ARP display name; when
// several match and publisher is given, those whose normalized publisher
// also matches are preferred.
func (s *Source) ByNormalizedName(name, publisher string) ([]*Package, error) {
	names := NormalizeName(name)
	if len(names) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	pkgs, err := s.collect(`SELECT DISTINCT `+pkgCols+` FROM norm_names2 n JOIN packages p ON p.rowid = n.package WHERE n.norm_name IN (`+ph+`)`, args...)
	if err != nil || len(pkgs) <= 1 || publisher == "" {
		return pkgs, err
	}
	np := NormalizePublisher(publisher)
	var filtered []*Package
	for _, p := range pkgs {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM norm_publishers2 WHERE package = ? AND norm_publisher = ?`, p.RowID, np).Scan(&n); err == nil && n > 0 {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) > 0 {
		return filtered, nil
	}
	return pkgs, nil
}

func (s *Source) collect(q string, args ...any) ([]*Package, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		p, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

var badID = regexp.MustCompile(`[/\\]|\.\.`)

// Versions returns the package's available versions, newest first.
func (p *Package) Versions(ctx context.Context) ([]VersionEntry, error) {
	if badID.MatchString(p.ID) || len(p.Hash) < 4 {
		return nil, fmt.Errorf("package %q has invalid index data", p.ID)
	}
	h8 := hex.EncodeToString(p.Hash)[:8]
	cache := filepath.Join(p.Source.dir, "packages", safeName(p.ID), h8+".yaml")
	data, err := os.ReadFile(cache)
	if err != nil {
		url := fmt.Sprintf("%s/packages/%s/%s/versionData.mszyml", strings.TrimRight(p.Source.Config.Arg, "/"), p.ID, h8)
		raw, err := httpx.GetBytes(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("fetch version data for %s: %w", p.ID, err)
		}
		if data, err = mszyml.Decode(raw); err != nil {
			return nil, fmt.Errorf("decode version data for %s: %w", p.ID, err)
		}
		if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
			_ = os.WriteFile(cache, data, 0o644)
		}
	}
	var vd versionData
	if err := yaml.Unmarshal(data, &vd); err != nil {
		return nil, fmt.Errorf("parse version data for %s: %w", p.ID, err)
	}
	vs := vd.Versions
	sortEntries(vs)
	return vs, nil
}

func sortEntries(vs []VersionEntry) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && version.Compare(vs[j].Version, vs[j-1].Version) > 0; j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}

// Manifest fetches (and verifies) the manifest for a version; an empty
// version means the latest.
func (p *Package) Manifest(ctx context.Context, ver string) (*manifest.Manifest, []byte, error) {
	vs, err := p.Versions(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(vs) == 0 {
		return nil, nil, fmt.Errorf("package %s has no versions", p.ID)
	}
	e := vs[0]
	if ver != "" {
		found := false
		for _, v := range vs {
			if version.Compare(v.Version, ver) == 0 {
				e, found = v, true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("version %s of %s not found", ver, p.ID)
		}
	}
	if strings.Contains(e.RelativePath, "..") || strings.Contains(e.RelativePath, `\`) {
		return nil, nil, fmt.Errorf("invalid manifest path %q", e.RelativePath)
	}
	want := strings.ToLower(e.SHA256)
	cache := filepath.Join(p.Source.dir, "manifests", safeName(want)+".yaml")
	b, err := os.ReadFile(cache)
	if err != nil || sha(b) != want {
		url := strings.TrimRight(p.Source.Config.Arg, "/") + "/" + strings.TrimLeft(e.RelativePath, "/")
		if b, err = httpx.GetBytes(ctx, url); err != nil {
			return nil, nil, fmt.Errorf("fetch manifest: %w", err)
		}
		if want != "" && sha(b) != want {
			return nil, nil, fmt.Errorf("manifest hash mismatch for %s %s", p.ID, e.Version)
		}
		if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
			_ = os.WriteFile(cache, b, 0o644)
		}
	}
	m, err := manifest.Parse(b)
	if err != nil {
		return nil, nil, err
	}
	return m, b, nil
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

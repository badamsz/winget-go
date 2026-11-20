package source

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winget-go/internal/config"
)

const testManifest = `PackageIdentifier: Contoso.Tool
PackageVersion: 2.0.0
PackageLocale: en-US
PackageName: Contoso Tool
Publisher: Contoso Ltd.
ShortDescription: A test tool.
Moniker: ctool
Tags: [testing]
Commands: [ctool]
Installers:
- Architecture: x64
  InstallerType: portable
  InstallerUrl: https://example.invalid/ctool.exe
  InstallerSha256: 0000000000000000000000000000000000000000000000000000000000000000
ManifestType: merged
ManifestVersion: 1.6.0
`

func mszip(data []byte) []byte {
	var blk bytes.Buffer
	blk.WriteString("CK")
	w, _ := flate.NewWriter(&blk, flate.BestCompression)
	w.Write(data)
	w.Close()
	var out bytes.Buffer
	hdr := make([]byte, 24)
	copy(hdr, []byte{0x0A, 0x51, 0xE5, 0xC0})
	binary.LittleEndian.PutUint64(hdr[8:], uint64(len(data)))
	binary.LittleEndian.PutUint64(hdr[16:], uint64(len(data)))
	out.Write(hdr)
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(blk.Len()))
	out.Write(n[:])
	out.Write(blk.Bytes())
	return out.Bytes()
}

// fakeCDN builds a minimal v2 source and serves it.
func fakeCDN(t *testing.T, manifestBody string) (*httptest.Server, *int) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("contoso"))
	stmts := []string{
		`CREATE TABLE metadata(name TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL)`,
		`INSERT INTO metadata VALUES('majorVersion','2'),('minorVersion','1')`,
		`CREATE TABLE packages(rowid INTEGER PRIMARY KEY, id TEXT NOT NULL, name TEXT NOT NULL, moniker TEXT, latest_version TEXT NOT NULL, arp_min_version TEXT, arp_max_version TEXT, hash BLOB)`,
		`CREATE TABLE tags2(rowid INTEGER PRIMARY KEY, tag TEXT NOT NULL)`,
		`CREATE TABLE tags2_map(tag INT64 NOT NULL, package INT64 NOT NULL)`,
		`CREATE TABLE commands2(rowid INTEGER PRIMARY KEY, command TEXT NOT NULL)`,
		`CREATE TABLE commands2_map(command INT64 NOT NULL, package INT64 NOT NULL)`,
		`CREATE TABLE productcodes2(productcode TEXT NOT NULL, package INT64 NOT NULL)`,
		`CREATE TABLE pfns2(pfn TEXT NOT NULL, package INT64 NOT NULL)`,
		`CREATE TABLE norm_names2(norm_name TEXT NOT NULL, package INT64 NOT NULL)`,
		`CREATE TABLE norm_publishers2(norm_publisher TEXT NOT NULL, package INT64 NOT NULL)`,
		`INSERT INTO tags2 VALUES(1,'testing')`, `INSERT INTO tags2_map VALUES(1,1)`,
		`INSERT INTO commands2 VALUES(1,'ctool')`, `INSERT INTO commands2_map VALUES(1,1)`,
		`INSERT INTO productcodes2 VALUES('{abc}',1)`,
		`INSERT INTO pfns2 VALUES('contoso.tool_8wekyb3d8bbwe',1)`,
		`INSERT INTO norm_names2 VALUES('contosotool',1)`,
		`INSERT INTO norm_publishers2 VALUES('contoso',1)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO packages VALUES(1,'Contoso.Tool','Contoso Tool','ctool','2.0.0',NULL,NULL,?)`, hash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO packages VALUES(2,'Fabrikam.ToolKit','Fabrikam ToolKit',NULL,'1.0',NULL,NULL,?)`, hash[:]); err != nil {
		t.Fatal(err)
	}
	db.Close()
	dbBytes, _ := os.ReadFile(dbPath)

	var msix bytes.Buffer
	zw := zip.NewWriter(&msix)
	f, _ := zw.Create("Public/index.db")
	f.Write(dbBytes)
	zw.Close()

	goodSum := sha256.Sum256([]byte(testManifest))
	vd := "sV: 1.0\nvD:\n- v: 1.0.0\n  rP: manifests/c/Contoso/Tool/1.0.0/aaaa\n  s256H: 00\n- v: 2.0.0\n  rP: manifests/c/Contoso/Tool/2.0.0/bbbb\n  s256H: " + hex.EncodeToString(goodSum[:]) + "\n"
	h8 := hex.EncodeToString(hash[:])[:8]
	sourceHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/source2.msix":
			sourceHits++
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			w.Write(msix.Bytes())
		case "/packages/Contoso.Tool/" + h8 + "/versionData.mszyml":
			w.Write(mszip([]byte(vd)))
		case "/manifests/c/Contoso/Tool/2.0.0/bbbb":
			io.WriteString(w, manifestBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &sourceHits
}

func TestSourceEndToEnd(t *testing.T) {
	t.Setenv("WINGET_GO_HOME", t.TempDir())
	srv, hits := fakeCDN(t, testManifest)
	cfg := config.SourceConfig{Name: "test", Type: config.PreIndexedType, Arg: srv.URL}
	ctx := context.Background()

	s, err := Open(ctx, cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rs, err := s.Search(Query{Term: "tool", Fields: DefaultSearchFields})
	if err != nil || len(rs) != 2 {
		t.Fatalf("search: %v %d", err, len(rs))
	}
	rs, _ = s.Search(Query{Term: "testing", Fields: []Field{FieldTag}})
	if len(rs) != 1 || rs[0].ID != "Contoso.Tool" || rs[0].Field != FieldTag {
		t.Fatalf("tag search: %+v", rs)
	}
	rs, _ = s.Search(Query{Term: "contoso.tool", Fields: DefaultInstallFields, Exact: true})
	if len(rs) != 1 || rs[0].MatchType != MatchCaseInsensitive {
		t.Fatalf("exact search: %+v", rs)
	}
	if rs, _ := s.Search(Query{Term: "%", Fields: DefaultInstallFields}); len(rs) != 0 {
		t.Fatalf("LIKE wildcard not escaped: %d results", len(rs))
	}

	p, _ := s.Lookup("contoso.TOOL")
	if p == nil {
		t.Fatal("lookup failed")
	}
	vs, err := p.Versions(ctx)
	if err != nil || len(vs) != 2 || vs[0].Version != "2.0.0" {
		t.Fatalf("versions: %v %+v", err, vs)
	}
	m, _, err := p.Manifest(ctx, "")
	if err != nil || m.PackageName != "Contoso Tool" {
		t.Fatalf("manifest: %v %+v", err, m)
	}
	if _, _, err := p.Manifest(ctx, "9.9"); err == nil {
		t.Fatal("expected missing version error")
	}

	if ps, _ := s.ByFamilyName("Contoso.Tool_8wekyb3d8bbwe"); len(ps) != 1 || ps[0].ID != "Contoso.Tool" {
		t.Errorf("ByFamilyName: %+v", ps)
	}
	if ps, _ := s.ByProductCode("{ABC}"); len(ps) != 1 {
		t.Fatalf("product code lookup: %d", len(ps))
	}
	if ps, _ := s.ByNormalizedName("Contoso Tool 2.0.0", "Contoso Ltd."); len(ps) != 1 {
		t.Fatalf("name lookup: %d", len(ps))
	}

	// Forced update sends no conditional headers; normal update gets a 304.
	if err := s.Update(ctx, false); err != nil {
		t.Fatal(err)
	}
	if *hits != 2 {
		t.Fatalf("source hits = %d", *hits)
	}
}

func TestManifestHashMismatch(t *testing.T) {
	t.Setenv("WINGET_GO_HOME", t.TempDir())
	srv, _ := fakeCDN(t, strings.Replace(testManifest, "A test tool.", "Tampered!", 1))
	s, err := Open(context.Background(), config.SourceConfig{Name: "t", Arg: srv.URL}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _ := s.Lookup("Contoso.Tool")
	if _, _, err := p.Manifest(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
}

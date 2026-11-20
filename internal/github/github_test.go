package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"winget-go/internal/manifest"
)

func TestParseReleaseURL(t *testing.T) {
	r, ok := ParseReleaseURL("https://github.com/ip7z/7zip/releases/download/26.06/7z2606-x64.exe")
	if !ok || r.Owner != "ip7z" || r.Repo != "7zip" || r.Tag != "26.06" || r.Asset != "7z2606-x64.exe" {
		t.Fatalf("got %+v %v", r, ok)
	}
	for _, bad := range []string{
		"https://example.com/a/b/releases/download/t/x.exe",
		"https://github.com/a/b/archive/refs/tags/v1.zip",
	} {
		if _, ok := ParseReleaseURL(bad); ok {
			t.Errorf("%s should not parse", bad)
		}
	}
}

func rel(tag string, names ...string) *Release {
	r := &Release{TagName: tag}
	for _, n := range names {
		r.Assets = append(r.Assets, Asset{Name: n, URL: "https://github.com/o/r/releases/download/" + tag + "/" + n})
	}
	return r
}

func TestMatchAsset(t *testing.T) {
	cases := []struct {
		old, oldTag, oldVer string
		r                   *Release
		want                string
	}{
		{"7z2606-x64.exe", "26.06", "26.06", rel("26.07", "7z2607.exe", "7z2607-x64.exe", "7z2607-arm64.exe"), "7z2607-x64.exe"},
		{"tool_v1.2.3_windows_amd64.zip", "v1.2.3", "1.2.3", rel("v1.3.0", "tool_v1.3.0_windows_amd64.zip", "tool_v1.3.0_linux_amd64.tar.gz"), "tool_v1.3.0_windows_amd64.zip"},
		{"app-1.2.3-setup.exe", "v1.2.3", "1.2.3", rel("v2.0.0", "app-2.0.0-setup.exe", "app-2.0.0.msi"), "app-2.0.0-setup.exe"},
		{"setup.exe", "v1", "1", rel("v2", "setup.exe"), "setup.exe"},
		// Structural fallback (version format changed: 1.2 -> 1.2.10).
		{"app-1.2-win64.msi", "1.2", "1.2", rel("1.2.10", "app-1.2.10-win64.msi"), "app-1.2.10-win64.msi"},
	}
	for _, c := range cases {
		a, err := MatchAsset(c.old, c.oldTag, c.oldVer, c.r)
		if err != nil {
			t.Errorf("%s: %v", c.old, err)
			continue
		}
		if a.Name != c.want {
			t.Errorf("%s: got %s want %s", c.old, a.Name, c.want)
		}
	}
	if _, err := MatchAsset("a-1-x64.exe", "1", "1", rel("2", "b-2.exe")); err == nil {
		t.Error("expected no match")
	}
}

func TestCheckUsesNewerRelease(t *testing.T) {
	t.Setenv("WINGET_GO_HOME", t.TempDir())
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/repos/ip7z/7zip/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		w.Write([]byte(`{"tag_name":"26.07","html_url":"https://github.com/ip7z/7zip/releases/tag/26.07",
		  "assets":[{"name":"7z2607-x64.exe","browser_download_url":"https://github.com/ip7z/7zip/releases/download/26.07/7z2607-x64.exe",
		  "digest":"sha256:ABCDEF"}]}`))
	}))
	defer srv.Close()
	old := APIBase
	APIBase = srv.URL
	defer func() { APIBase = old }()

	m := &manifest.Manifest{PackageIdentifier: "7zip.7zip", PackageVersion: "26.06"}
	inst := manifest.Installer{InstallerUrl: "https://github.com/ip7z/7zip/releases/download/26.06/7z2606-x64.exe", InstallerSha256: "OLD"}
	o, err := Check(context.Background(), m, &inst)
	if err != nil || o == nil {
		t.Fatalf("Check: %v %v", o, err)
	}
	if o.Version != "26.07" {
		t.Fatalf("version %s", o.Version)
	}
	got := o.Apply(inst)
	if got.InstallerSha256 != "abcdef" || got.InstallerUrl != "https://github.com/ip7z/7zip/releases/download/26.07/7z2607-x64.exe" {
		t.Fatalf("apply: %+v", got)
	}
	// Second call within TTL is served from cache.
	if _, err := Check(context.Background(), m, &inst); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 API call, got %d", calls.Load())
	}

	// Same version as manifest -> no override.
	m.PackageVersion = "26.07"
	if o, _ := Check(context.Background(), m, &inst); o != nil {
		t.Fatal("expected no override")
	}
}

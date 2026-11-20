// Package github optionally augments winget manifests with data from
// GitHub Releases. Many packages in the community repository download
// their installers from github.com/<owner>/<repo>/releases/download/...;
// for those we can look up the repository's latest release, which often
// lands before the corresponding winget manifest is merged.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"winget-go/internal/config"
	"winget-go/internal/httpx"
	"winget-go/internal/manifest"
	"winget-go/internal/version"
)

// APIBase can be overridden in tests.
var APIBase = "https://api.github.com"

// CacheTTL is how long a release lookup is reused without contacting
// GitHub at all. After that, a conditional request is made (304s do not
// count against the API rate limit).
var CacheTTL = time.Hour

// Ref identifies a release asset by URL.
type Ref struct {
	Owner, Repo, Tag, Asset string
}

// ParseReleaseURL recognizes
// https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>.
func ParseReleaseURL(raw string) (Ref, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return Ref{}, false
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" {
		return Ref{}, false
	}
	un := func(s string) string { v, _ := url.PathUnescape(s); return v }
	return Ref{Owner: un(parts[0]), Repo: un(parts[1]), Tag: un(parts[4]), Asset: un(parts[5])}, true
}

// Asset is a release asset.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // "sha256:<hex>" when GitHub has computed it
}

// SHA256 returns the asset's SHA-256 from its digest, if known.
func (a *Asset) SHA256() string {
	if d, ok := strings.CutPrefix(a.Digest, "sha256:"); ok {
		return strings.ToLower(d)
	}
	return ""
}

// Release is a GitHub release.
type Release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Assets      []Asset `json:"assets"`
}

type cached struct {
	ETag    string    `json:"etag"`
	Fetched time.Time `json:"fetched"`
	Body    []byte    `json:"body"`
}

// Latest returns the latest (non-prerelease, non-draft) release.
func Latest(ctx context.Context, owner, repo string) (*Release, error) {
	safe := regexp.MustCompile(`[^A-Za-z0-9._-]`)
	cachePath := filepath.Join(config.CacheDir(), "github",
		safe.ReplaceAllString(owner, "_")+"__"+safe.ReplaceAllString(repo, "_")+".json")
	var c cached
	if b, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if len(c.Body) == 0 || time.Since(c.Fetched) > CacheTTL {
		body, etag, err := fetch(ctx, fmt.Sprintf("%s/repos/%s/%s/releases/latest",
			APIBase, url.PathEscape(owner), url.PathEscape(repo)), c.ETag)
		switch {
		case err == nil && body == nil: // 304
			c.Fetched = time.Now()
		case err == nil:
			c = cached{ETag: etag, Fetched: time.Now(), Body: body}
		case len(c.Body) == 0:
			return nil, err
		}
		if err == nil {
			if b, e := json.Marshal(c); e == nil && os.MkdirAll(filepath.Dir(cachePath), 0o755) == nil {
				_ = os.WriteFile(cachePath, b, 0o644)
			}
		}
	}
	var r Release
	if err := json.Unmarshal(c.Body, &r); err != nil {
		return nil, fmt.Errorf("github: decode release: %w", err)
	}
	return &r, nil
}

func fetch(ctx context.Context, u, etag string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := httpx.NewRequest(ctx, u)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if tok := token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, etag, nil
	case http.StatusOK:
		b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		return b, resp.Header.Get("ETag"), err
	case http.StatusNotFound:
		return nil, "", errors.New("github: no published release")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, "", errors.New("github: API rate limit reached (set GITHUB_TOKEN to raise it)")
	}
	return nil, "", fmt.Errorf("github: %s", resp.Status)
}

func token() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

var leadingNonDigits = regexp.MustCompile(`^[^0-9]*`)

// TagVersion turns a tag like "v1.2.3" or "release-1.2.3" into "1.2.3".
func TagVersion(tag string) string {
	return leadingNonDigits.ReplaceAllString(tag, "")
}

func variants(v string) []string {
	out := []string{v}
	for _, sep := range []string{"", "_", "-"} {
		if w := strings.ReplaceAll(v, ".", sep); w != v {
			out = append(out, w)
		}
	}
	return out
}

var digits = regexp.MustCompile(`\d+`)

// MatchAsset finds the asset in rel that corresponds to oldAsset (an
// asset name from the release identified by oldTag / oldVersion).
func MatchAsset(oldAsset, oldTag, oldVersion string, rel *Release) (*Asset, error) {
	byName := map[string]*Asset{}
	for i := range rel.Assets {
		byName[rel.Assets[i].Name] = &rel.Assets[i]
	}
	if a, ok := byName[oldAsset]; ok { // version-less asset names
		return a, nil
	}
	newV := TagVersion(rel.TagName)
	olds := map[string]bool{}
	for _, v := range []string{oldTag, TagVersion(oldTag), oldVersion} {
		if v != "" {
			olds[v] = true
		}
	}
	for ov := range olds {
		ovs, nvs := variants(ov), variants(newV)
		if ov == oldTag {
			nvs = variants(rel.TagName)
		}
		for i, o := range ovs {
			if i >= len(nvs) || !strings.Contains(oldAsset, o) {
				continue
			}
			if a, ok := byName[strings.ReplaceAll(oldAsset, o, nvs[i])]; ok {
				return a, nil
			}
		}
	}
	// Fall back to structural similarity: same name with digit runs masked.
	shape := digits.ReplaceAllString(oldAsset, "#")
	var hits []*Asset
	for i := range rel.Assets {
		if digits.ReplaceAllString(rel.Assets[i].Name, "#") == shape {
			hits = append(hits, &rel.Assets[i])
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	names := make([]string, 0, len(rel.Assets))
	for _, a := range rel.Assets {
		names = append(names, a.Name)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("github: no asset in %s corresponds to %s (assets: %s)", rel.TagName, oldAsset, strings.Join(names, ", "))
}

// Override describes a newer installer found on GitHub.
type Override struct {
	Ref     Ref
	Release *Release
	Asset   *Asset
	Version string // version derived from the release tag
}

// ReleaseInfo returns the latest release for a manifest's GitHub-hosted
// installers, if any installer is hosted on GitHub.
func ReleaseInfo(ctx context.Context, m *manifest.Manifest) (*Release, Ref, error) {
	for _, in := range m.Installers {
		if ref, ok := ParseReleaseURL(in.InstallerUrl); ok {
			rel, err := Latest(ctx, ref.Owner, ref.Repo)
			return rel, ref, err
		}
	}
	return nil, Ref{}, nil
}

// Check returns an Override if inst is hosted on GitHub and the
// repository's latest release is newer than the manifest version.
// It returns (nil, nil) when GitHub has nothing newer.
func Check(ctx context.Context, m *manifest.Manifest, inst *manifest.Installer) (*Override, error) {
	ref, ok := ParseReleaseURL(inst.InstallerUrl)
	if !ok {
		return nil, nil
	}
	rel, err := Latest(ctx, ref.Owner, ref.Repo)
	if err != nil {
		return nil, err
	}
	if rel.Draft || rel.Prerelease || rel.TagName == ref.Tag {
		return nil, nil
	}
	newV := TagVersion(rel.TagName)
	if newV == "" || version.Compare(newV, m.PackageVersion) <= 0 || version.Compare(newV, TagVersion(ref.Tag)) <= 0 {
		return nil, nil
	}
	a, err := MatchAsset(ref.Asset, ref.Tag, m.PackageVersion, rel)
	if err != nil {
		return nil, err
	}
	return &Override{Ref: ref, Release: rel, Asset: a, Version: newV}, nil
}

// Apply returns a copy of inst pointing at the override's asset. The
// SHA-256 comes from GitHub's asset digest; it is empty when GitHub has
// not published one, and callers must decide whether to proceed.
func (o *Override) Apply(inst manifest.Installer) manifest.Installer {
	inst.InstallerUrl = o.Asset.URL
	inst.InstallerSha256 = o.Asset.SHA256()
	// The ARP product code of MSI installers changes between versions.
	if t := strings.ToLower(inst.InstallerType); t == "msi" || t == "wix" {
		inst.ProductCode = ""
		inst.AppsAndFeaturesEntries = nil
	}
	return inst
}

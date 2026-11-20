// Package manifest models winget "merged" manifests as served by the
// pre-indexed source CDN (one YAML file combining version, defaultLocale,
// locale and installer manifests).
package manifest

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Agreement is a license/terms entry the user must accept.
type Agreement struct {
	AgreementLabel string `yaml:"AgreementLabel"`
	Agreement      string `yaml:"Agreement"`
	AgreementUrl   string `yaml:"AgreementUrl"`
}

// Documentation is a link to package docs.
type Documentation struct {
	DocumentLabel string `yaml:"DocumentLabel"`
	DocumentUrl   string `yaml:"DocumentUrl"`
}

// Locale holds the localizable package metadata.
type Locale struct {
	PackageLocale       string          `yaml:"PackageLocale"`
	Publisher           string          `yaml:"Publisher"`
	PublisherUrl        string          `yaml:"PublisherUrl"`
	PublisherSupportUrl string          `yaml:"PublisherSupportUrl"`
	PrivacyUrl          string          `yaml:"PrivacyUrl"`
	Author              string          `yaml:"Author"`
	PackageName         string          `yaml:"PackageName"`
	PackageUrl          string          `yaml:"PackageUrl"`
	License             string          `yaml:"License"`
	LicenseUrl          string          `yaml:"LicenseUrl"`
	Copyright           string          `yaml:"Copyright"`
	CopyrightUrl        string          `yaml:"CopyrightUrl"`
	ShortDescription    string          `yaml:"ShortDescription"`
	Description         string          `yaml:"Description"`
	Moniker             string          `yaml:"Moniker"`
	Tags                []string        `yaml:"Tags"`
	ReleaseNotes        string          `yaml:"ReleaseNotes"`
	ReleaseNotesUrl     string          `yaml:"ReleaseNotesUrl"`
	PurchaseUrl         string          `yaml:"PurchaseUrl"`
	InstallationNotes   string          `yaml:"InstallationNotes"`
	Agreements          []Agreement     `yaml:"Agreements"`
	Documentations      []Documentation `yaml:"Documentations"`
}

// Switches are installer command-line switches.
type Switches struct {
	Silent             string `yaml:"Silent"`
	SilentWithProgress string `yaml:"SilentWithProgress"`
	Interactive        string `yaml:"Interactive"`
	InstallLocation    string `yaml:"InstallLocation"`
	Log                string `yaml:"Log"`
	Upgrade            string `yaml:"Upgrade"`
	Custom             string `yaml:"Custom"`
	Repair             string `yaml:"Repair"`
}

// ExpectedReturnCode maps a non-zero installer exit code to a meaning.
type ExpectedReturnCode struct {
	InstallerReturnCode int64  `yaml:"InstallerReturnCode"`
	ReturnResponse      string `yaml:"ReturnResponse"`
	ReturnResponseUrl   string `yaml:"ReturnResponseUrl"`
}

// NestedInstallerFile describes a file inside an archive installer.
type NestedInstallerFile struct {
	RelativeFilePath     string `yaml:"RelativeFilePath"`
	PortableCommandAlias string `yaml:"PortableCommandAlias"`
}

// PackageDependency is a dependency on another winget package.
type PackageDependency struct {
	PackageIdentifier string `yaml:"PackageIdentifier"`
	MinimumVersion    string `yaml:"MinimumVersion"`
}

// Dependencies lists what a package needs.
type Dependencies struct {
	WindowsFeatures      []string            `yaml:"WindowsFeatures"`
	WindowsLibraries     []string            `yaml:"WindowsLibraries"`
	PackageDependencies  []PackageDependency `yaml:"PackageDependencies"`
	ExternalDependencies []string            `yaml:"ExternalDependencies"`
}

// AppsAndFeaturesEntry describes the ARP entry an installer creates.
type AppsAndFeaturesEntry struct {
	DisplayName    string `yaml:"DisplayName"`
	Publisher      string `yaml:"Publisher"`
	DisplayVersion string `yaml:"DisplayVersion"`
	ProductCode    string `yaml:"ProductCode"`
	UpgradeCode    string `yaml:"UpgradeCode"`
	InstallerType  string `yaml:"InstallerType"`
}

// InstallationMetadata holds install location hints.
type InstallationMetadata struct {
	DefaultInstallLocation string `yaml:"DefaultInstallLocation"`
}

// InstallerCommon are the fields that may appear either at the manifest
// root (as defaults) or on an individual installer.
type InstallerCommon struct {
	InstallerLocale             string                 `yaml:"InstallerLocale"`
	Platform                    []string               `yaml:"Platform"`
	MinimumOSVersion            string                 `yaml:"MinimumOSVersion"`
	InstallerType               string                 `yaml:"InstallerType"`
	NestedInstallerType         string                 `yaml:"NestedInstallerType"`
	NestedInstallerFiles        []NestedInstallerFile  `yaml:"NestedInstallerFiles"`
	Scope                       string                 `yaml:"Scope"`
	InstallModes                []string               `yaml:"InstallModes"`
	InstallerSwitches           Switches               `yaml:"InstallerSwitches"`
	InstallerSuccessCodes       []int64                `yaml:"InstallerSuccessCodes"`
	ExpectedReturnCodes         []ExpectedReturnCode   `yaml:"ExpectedReturnCodes"`
	UpgradeBehavior             string                 `yaml:"UpgradeBehavior"`
	Commands                    []string               `yaml:"Commands"`
	Protocols                   []string               `yaml:"Protocols"`
	FileExtensions              []string               `yaml:"FileExtensions"`
	Dependencies                Dependencies           `yaml:"Dependencies"`
	PackageFamilyName           string                 `yaml:"PackageFamilyName"`
	ProductCode                 string                 `yaml:"ProductCode"`
	ReleaseDate                 string                 `yaml:"ReleaseDate"`
	InstallerAbortsTerminal     bool                   `yaml:"InstallerAbortsTerminal"`
	ElevationRequirement        string                 `yaml:"ElevationRequirement"`
	AppsAndFeaturesEntries      []AppsAndFeaturesEntry `yaml:"AppsAndFeaturesEntries"`
	InstallationMetadata        InstallationMetadata   `yaml:"InstallationMetadata"`
	ArchiveBinariesDependOnPath bool                   `yaml:"ArchiveBinariesDependOnPath"`
}

// Installer is a single downloadable installer.
type Installer struct {
	Architecture    string `yaml:"Architecture"`
	InstallerUrl    string `yaml:"InstallerUrl"`
	InstallerSha256 string `yaml:"InstallerSha256"`
	SignatureSha256 string `yaml:"SignatureSha256"`
	InstallerCommon `yaml:",inline"`
}

// Manifest is a merged winget manifest.
type Manifest struct {
	PackageIdentifier string `yaml:"PackageIdentifier"`
	PackageVersion    string `yaml:"PackageVersion"`
	Channel           string `yaml:"Channel"`
	ManifestType      string `yaml:"ManifestType"`
	ManifestVersion   string `yaml:"ManifestVersion"`
	Locale            `yaml:",inline"`
	InstallerCommon   `yaml:",inline"`
	Installers        []Installer `yaml:"Installers"`
	// Merged manifests use "Localization"; multi-file ones "Localizations".
	Localization  []Locale `yaml:"Localization"`
	Localizations []Locale `yaml:"Localizations"`
}

// Parse decodes a merged manifest and applies root-level installer
// defaults to each installer.
func Parse(b []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.PackageIdentifier == "" {
		return nil, fmt.Errorf("parse manifest: missing PackageIdentifier")
	}
	m.Localizations = append(m.Localizations, m.Localization...)
	m.Localization = nil
	for i := range m.Installers {
		mergeDefaults(&m.Installers[i].InstallerCommon, &m.InstallerCommon)
	}
	return &m, nil
}

// mergeDefaults copies every zero-valued field of dst from def. Switches
// are merged per key.
func mergeDefaults(dst, def *InstallerCommon) {
	dv, sv := reflect.ValueOf(dst).Elem(), reflect.ValueOf(def).Elem()
	for i := 0; i < dv.NumField(); i++ {
		f := dv.Field(i)
		if f.Type() == reflect.TypeOf(Switches{}) {
			mergeZero(f, sv.Field(i))
			continue
		}
		if f.IsZero() {
			f.Set(sv.Field(i))
		}
	}
}

func mergeZero(dst, src reflect.Value) {
	for i := 0; i < dst.NumField(); i++ {
		if dst.Field(i).IsZero() {
			dst.Field(i).Set(src.Field(i))
		}
	}
}

// Localized returns the default locale overlaid with the best matching
// localization for the requested locale (e.g. "de-DE" or "de").
func (m *Manifest) Localized(locale string) Locale {
	out := m.Locale
	if locale == "" || strings.EqualFold(locale, m.PackageLocale) {
		return out
	}
	var best *Locale
	for i := range m.Localizations {
		l := &m.Localizations[i]
		if strings.EqualFold(l.PackageLocale, locale) {
			best = l
			break
		}
		if best == nil && langOf(l.PackageLocale) == langOf(locale) {
			best = l
		}
	}
	if best != nil {
		l := *best
		mergeZero(reflect.ValueOf(&l).Elem(), reflect.ValueOf(&out).Elem())
		return l
	}
	return out
}

func langOf(l string) string {
	l = strings.ToLower(l)
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		return l[:i]
	}
	return l
}

// Name returns the best display name for the package.
func (m *Manifest) Name() string {
	if m.PackageName != "" {
		return m.PackageName
	}
	return m.PackageIdentifier
}

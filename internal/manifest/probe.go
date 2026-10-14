package manifest

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultManifestVersion is the pinned F5 BNK release manifest version.
const DefaultManifestVersion = "2.4.0-3.2882.0"

// ReleaseManifest represents the parsed BOM from F5's release manifest chart.
type ReleaseManifest struct {
	HelmRepo    string            `yaml:"f5_helm_repo" json:"helmRepo"`
	DockerRepo  string            `yaml:"f5_docker_repo" json:"dockerRepo"`
	Version     string            `yaml:"version" json:"version"`
	HelmCharts  map[string]string `yaml:"helm_charts" json:"helmCharts"`
	DockerImgs  map[string]string `yaml:"docker_images" json:"dockerImages"`
	RawManifest []byte            `yaml:"-" json:"-"`
}

type rawReleaseManifest struct {
	F5HelmRepo   string `yaml:"f5_helm_repo"`
	F5DockerRepo string `yaml:"f5_docker_repo"`
	Releases     []struct {
		Version    string `yaml:"version"`
		HelmCharts []struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
		} `yaml:"helm_charts"`
		DockerImages []struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
		} `yaml:"docker_images"`
	} `yaml:"releases"`
}

// ParseReleaseManifest parses raw YAML bytes into a structured ReleaseManifest.
func ParseReleaseManifest(body []byte) (*ReleaseManifest, error) {
	var raw rawReleaseManifest
	if err := yaml.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse release-manifest yaml: %w", err)
	}
	if len(raw.Releases) == 0 {
		return nil, fmt.Errorf("release-manifest has no releases[] entries")
	}
	rel := raw.Releases[0]
	if rel.Version == "" {
		return nil, fmt.Errorf("release-manifest releases[0].version is empty")
	}
	m := &ReleaseManifest{
		HelmRepo:    raw.F5HelmRepo,
		DockerRepo:  raw.F5DockerRepo,
		Version:     rel.Version,
		HelmCharts:  make(map[string]string, len(rel.HelmCharts)),
		DockerImgs:  make(map[string]string, len(rel.DockerImages)),
		RawManifest: body,
	}
	for _, c := range rel.HelmCharts {
		if c.Name != "" {
			m.HelmCharts[c.Name] = c.Version
		}
	}
	for _, i := range rel.DockerImages {
		if i.Name != "" {
			m.DockerImgs[i.Name] = i.Version
		}
	}
	return m, nil
}

// PrintSummary outputs a concise summary of the manifest.
func (m *ReleaseManifest) PrintSummary(w io.Writer) {
	fmt.Fprintf(w, "  F5 Helm Repo:   %s\n", m.HelmRepo)
	fmt.Fprintf(w, "  F5 Docker Repo: %s\n", m.DockerRepo)
	fmt.Fprintf(w, "  BOM Version:    %s\n", m.Version)
	fmt.Fprintf(w, "  Helm Charts:    %d\n", len(m.HelmCharts))
	fmt.Fprintf(w, "  Docker Images:  %d\n\n", len(m.DockerImgs))

	fmt.Fprintln(w, "Key Components:")
	keyCharts := []string{"f5-bigip-k8s-manifest", "f5-spk-ingress", "f5-tmm-controller", "cert-gen"}
	for _, kc := range keyCharts {
		if v, ok := m.HelmCharts[kc]; ok {
			fmt.Fprintf(w, "  • %-25s %s\n", kc, v)
		}
	}
}

// PrintFullBOM outputs every helm chart and container image in the manifest.
func (m *ReleaseManifest) PrintFullBOM(w io.Writer) {
	fmt.Fprintln(w, "\nAll Helm Charts:")
	chartKeys := make([]string, 0, len(m.HelmCharts))
	for k := range m.HelmCharts {
		chartKeys = append(chartKeys, k)
	}
	sort.Strings(chartKeys)
	for _, k := range chartKeys {
		fmt.Fprintf(w, "  %-32s %s\n", k, m.HelmCharts[k])
	}

	fmt.Fprintln(w, "\nAll Container Images:")
	imgKeys := make([]string, 0, len(m.DockerImgs))
	for k := range m.DockerImgs {
		imgKeys = append(imgKeys, k)
	}
	sort.Strings(imgKeys)
	for _, k := range imgKeys {
		fmt.Fprintf(w, "  %-32s %s\n", k, m.DockerImgs[k])
	}
}

// ProbeArchive attempts to extract and parse manifest.yaml from a given tarball or directory.
func ProbeArchive(path string) (*ReleaseManifest, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read path %s: %w", path, err)
	}

	if fi.IsDir() {
		manifestFile := filepath.Join(path, "manifest.yaml")
		data, err := os.ReadFile(manifestFile) // #nosec G304 -- path is operator-supplied local directory
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", manifestFile, err)
		}
		return ParseReleaseManifest(data)
	}

	f, err := os.Open(path) // #nosec G304 -- path is operator-supplied local tarball
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		if strings.HasSuffix(hdr.Name, "manifest.yaml") {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read manifest.yaml in tarball: %w", err)
			}
			return ParseReleaseManifest(data)
		}
	}

	return nil, fmt.Errorf("manifest.yaml not found inside archive %s", path)
}

// GetEmbeddedManifest returns a built-in baseline manifest for inspection.
func GetEmbeddedManifest() *ReleaseManifest {
	return &ReleaseManifest{
		HelmRepo:   "https://repo.f5.com/helm",
		DockerRepo: "repo.f5.com/f5-bnk",
		Version:    DefaultManifestVersion,
		HelmCharts: map[string]string{
			"f5-bigip-k8s-manifest": DefaultManifestVersion,
			"f5-spk-ingress":        "0.0.392",
			"f5-tmm-controller":     "0.0.392",
			"cert-gen":              "0.0.120",
		},
		DockerImgs: map[string]string{
			"f5-cne-controller": DefaultManifestVersion,
			"f5-tmm":            DefaultManifestVersion,
			"f5-cert-gen":       "0.0.120",
		},
	}
}

// Probe probes a manifest version against a local file/archive or returns the embedded baseline.
func Probe(_ context.Context, version string, archivePath string) (*ReleaseManifest, error) {
	if archivePath != "" {
		return ProbeArchive(archivePath)
	}

	m := GetEmbeddedManifest()
	if version != "" && version != m.Version {
		// Mock dynamic release for verification of version tags
		m.Version = version
		m.HelmCharts["f5-bigip-k8s-manifest"] = version
	}
	return m, nil
}

// Release records what gkebnkctl needs to know about one BNK release manifest
// without pulling it: the F5 Lifecycle Operator chart that ships with it.
type Release struct {
	Version  string
	FLOChart string
}

// KnownReleases lists every BNK release manifest gkebnkctl has been exercised against.
var KnownReleases = []Release{
	{Version: "2.3.0-3.2598.3-0.0.170", FLOChart: "v2.21.13-0.0.28"},
	{Version: "2.3.1-3.2598.3-0.0.304", FLOChart: "v2.21.13-0.0.53"},
	{Version: "2.3.2-3.2598.3-0.0.392", FLOChart: "v2.21.13-0.0.58"},
	{Version: "2.3.3-3.2598.3-0.0.509", FLOChart: "v2.21.13-0.0.64"},
	{Version: "2.4.0", FLOChart: "v2.30.0-0.5.2"},
	{Version: "2.4.0-3.2882.0", FLOChart: "v2.30.0-0.5.2"},
}

// FLOChartFor returns the FLO chart version paired with manifestVersion.
func FLOChartFor(manifestVersion string) (chart string, ok bool) {
	for _, r := range KnownReleases {
		if r.Version == manifestVersion {
			return r.FLOChart, true
		}
	}
	return "", false
}

// DefaultFLOChart is the FLO chart paired with DefaultManifestVersion.
func DefaultFLOChart() string {
	chart, _ := FLOChartFor(DefaultManifestVersion)
	return chart
}

// IsKnownRelease reports whether manifestVersion is a known release.
func IsKnownRelease(manifestVersion string) bool {
	_, ok := FLOChartFor(manifestVersion)
	return ok
}

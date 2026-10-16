package manifest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
)

const (
	FARRegistryHost      = "repo.f5.com"
	ReleaseManifestRepo  = "oci://repo.f5.com/release"
	ReleaseManifestChart = "f5-bigip-k8s-manifest"
	// DefaultManifestVersion is the pinned F5 BNK release manifest version.
	DefaultManifestVersion = "2.4.0-3.2882.0"
)

// Component represents a single component chart/image entry inside a BNK release manifest.
type Component struct {
	Name         string `yaml:"name" json:"name"`
	FullName     string `yaml:"full-name" json:"fullName"`
	ChartVersion string `yaml:"chart-version" json:"chartVersion"`
}

// ReleaseManifest represents the parsed BOM from F5's release manifest chart.
type ReleaseManifest struct {
	HelmRepo        string            `yaml:"f5_helm_repo" json:"helmRepo"`
	DockerRepo      string            `yaml:"f5_docker_repo" json:"dockerRepo"`
	Version         string            `yaml:"version" json:"version"`
	FLOVersion      string            `yaml:"flo_version,omitempty" json:"floVersion,omitempty"`
	Components      []Component       `yaml:"-" json:"components,omitempty"`
	HelmCharts      map[string]string `yaml:"helm_charts" json:"helmCharts"`
	DockerImgs      map[string]string `yaml:"docker_images" json:"dockerImages"`
	RawManifest     []byte            `yaml:"-" json:"-"`
	RawManifestYAML string            `yaml:"-" json:"rawManifestYaml,omitempty"`
	ChartNotes      string            `yaml:"-" json:"chartNotes,omitempty"`
}

func (m *ReleaseManifest) Chart(name string) string { return m.HelmCharts[name] }
func (m *ReleaseManifest) Image(name string) string { return m.DockerImgs[name] }
func (m *ReleaseManifest) RawYAML() []byte          { return m.RawManifest }

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
		Components []struct {
			Name     string `yaml:"name"`
			Version  string `yaml:"version"`
			Type     string `yaml:"type"`
			FullName string `yaml:"fullName"`
		} `yaml:"components"`
	} `yaml:"releases"`
}

// ParseReleaseManifest parses raw YAML bytes (either chart-versions or releases[0] format)
// into a structured ReleaseManifest.
func ParseReleaseManifest(body []byte) (*ReleaseManifest, error) {
	var parsedMap struct {
		Version       string               `yaml:"version"`
		ChartVersions map[string]Component `yaml:"chart-versions"`
	}
	if err := yaml.Unmarshal(body, &parsedMap); err == nil && len(parsedMap.ChartVersions) > 0 {
		rm := &ReleaseManifest{
			HelmRepo:        "oci://repo.f5.com/charts",
			DockerRepo:      "repo.f5.com/images",
			Version:         parsedMap.Version,
			RawManifest:     body,
			RawManifestYAML: string(body),
			HelmCharts:      make(map[string]string, len(parsedMap.ChartVersions)),
			DockerImgs:      make(map[string]string),
		}
		keys := make([]string, 0, len(parsedMap.ChartVersions))
		for k := range parsedMap.ChartVersions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			comp := parsedMap.ChartVersions[k]
			if comp.Name == "" {
				comp.Name = k
			}
			rm.Components = append(rm.Components, comp)
			rm.HelmCharts[comp.Name] = comp.ChartVersion
			if comp.Name == "f5-lifecycle-operator" || strings.Contains(comp.FullName, "f5-lifecycle-operator") {
				rm.FLOVersion = comp.ChartVersion
			}
		}
		return rm, nil
	}

	var parsedList struct {
		Version       string `yaml:"version"`
		ChartVersions []struct {
			Name         string `yaml:"name"`
			Version      string `yaml:"version"`
			ChartVersion string `yaml:"chart-version"`
			FullName     string `yaml:"full-name"`
		} `yaml:"chart-versions"`
		ImageVersions []struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
		} `yaml:"image-versions"`
	}
	if err := yaml.Unmarshal(body, &parsedList); err == nil && (len(parsedList.ChartVersions) > 0 || len(parsedList.ImageVersions) > 0) {
		rm := &ReleaseManifest{
			HelmRepo:        "oci://repo.f5.com/charts",
			DockerRepo:      "repo.f5.com/images",
			Version:         parsedList.Version,
			RawManifest:     body,
			RawManifestYAML: string(body),
			HelmCharts:      make(map[string]string, len(parsedList.ChartVersions)),
			DockerImgs:      make(map[string]string, len(parsedList.ImageVersions)),
		}
		for _, c := range parsedList.ChartVersions {
			v := c.Version
			if v == "" {
				v = c.ChartVersion
			}
			if c.Name != "" {
				rm.HelmCharts[c.Name] = v
				rm.Components = append(rm.Components, Component{
					Name:         c.Name,
					FullName:     c.FullName,
					ChartVersion: v,
				})
				if c.Name == "f5-lifecycle-operator" || strings.Contains(c.FullName, "f5-lifecycle-operator") {
					rm.FLOVersion = v
				}
			}
		}
		for _, img := range parsedList.ImageVersions {
			if img.Name != "" {
				rm.DockerImgs[img.Name] = img.Version
			}
		}
		return rm, nil
	}

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
		HelmRepo:        raw.F5HelmRepo,
		DockerRepo:      raw.F5DockerRepo,
		Version:         rel.Version,
		HelmCharts:      make(map[string]string, len(rel.HelmCharts)),
		DockerImgs:      make(map[string]string, len(rel.DockerImages)),
		RawManifest:     body,
		RawManifestYAML: string(body),
	}
	for _, c := range rel.HelmCharts {
		if c.Name != "" {
			m.HelmCharts[c.Name] = c.Version
			m.Components = append(m.Components, Component{
				Name:         c.Name,
				FullName:     c.Name,
				ChartVersion: c.Version,
			})
			if c.Name == "f5-lifecycle-operator" || c.Name == "charts/f5-lifecycle-operator" {
				m.FLOVersion = c.Version
			}
		}
	}
	for _, i := range rel.DockerImages {
		if i.Name != "" {
			m.DockerImgs[i.Name] = i.Version
		}
	}
	for _, comp := range rel.Components {
		if comp.Name == "" {
			continue
		}
		switch comp.Type {
		case "container", "image", "docker":
			m.DockerImgs[comp.Name] = comp.Version
		default:
			m.HelmCharts[comp.Name] = comp.Version
			m.Components = append(m.Components, Component{
				Name:         comp.Name,
				FullName:     comp.FullName,
				ChartVersion: comp.Version,
			})
			if comp.Name == "f5-lifecycle-operator" || strings.Contains(comp.FullName, "f5-lifecycle-operator") {
				m.FLOVersion = comp.Version
			}
		}
	}
	if m.FLOVersion == "" {
		if flo, ok := FLOChartFor(m.Version); ok {
			m.FLOVersion = flo
		}
	}
	return m, nil
}

// PrintSummary outputs a concise summary of the manifest.
func (m *ReleaseManifest) PrintSummary(w io.Writer) {
	fmt.Fprintf(w, "  F5 Helm Repo:   %s\n", m.HelmRepo)
	fmt.Fprintf(w, "  F5 Docker Repo: %s\n", m.DockerRepo)
	fmt.Fprintf(w, "  BOM Version:    %s\n", m.Version)
	if m.FLOVersion != "" {
		fmt.Fprintf(w, "  FLO Chart:      %s\n", m.FLOVersion)
	}
	fmt.Fprintf(w, "  Helm Charts:    %d\n", len(m.HelmCharts))
	fmt.Fprintf(w, "  Docker Images:  %d\n\n", len(m.DockerImgs))

	fmt.Fprintln(w, "Key Components:")
	keyCharts := []string{"f5-bigip-k8s-manifest", "f5-lifecycle-operator", "f5-spk-ingress", "f5-tmm-controller", "cert-gen"}
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

// ExtractFARAuth extracts the username and password for repo.f5.com from a FAR archive.
// Supports both .tar.gz / .tgz archives containing cne_pull_64.json, dockerconfigjson, or raw base64 JSON.
func ExtractFARAuth(farPath string) (username, password string, err error) {
	data, err := os.ReadFile(farPath) // #nosec G304 -- operator-supplied path via cluster.yaml or CLI
	if err != nil {
		return "", "", fmt.Errorf("read FAR file %s: %w", farPath, err)
	}

	if isGzipTar(data) {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			defer gz.Close()
			tr := tar.NewReader(gz)
			for {
				hdr, err := tr.Next()
				if err != nil {
					break
				}
				if hdr.Typeflag == tar.TypeReg {
					content, err := io.ReadAll(tr)
					if err == nil && len(content) > 0 {
						data = content
						break
					}
				}
			}
		}
	}

	var dockerCfg struct {
		Auths map[string]struct {
			Auth     string `json:"auth"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}
	if json.Unmarshal(data, &dockerCfg) == nil && len(dockerCfg.Auths) > 0 {
		for _, entry := range dockerCfg.Auths {
			if entry.Username != "" && entry.Password != "" {
				return entry.Username, entry.Password, nil
			}
			if entry.Auth != "" {
				if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(entry.Auth)); err == nil {
					if u, p, ok := strings.Cut(string(dec), ":"); ok {
						return u, p, nil
					}
				}
			}
		}
	}

	contentStr := strings.TrimSpace(string(data))
	decoded, err := base64.StdEncoding.DecodeString(contentStr)
	if err == nil {
		var sa map[string]any
		if json.Unmarshal(decoded, &sa) == nil && sa["type"] == "service_account" {
			return "_json_key", string(decoded), nil
		}
	}

	var sa map[string]any
	if json.Unmarshal(data, &sa) == nil && sa["type"] == "service_account" {
		return "_json_key", string(data), nil
	}

	return "_json_key_base64", contentStr, nil
}

func isGzipTar(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

// PullReleaseManifest logs into repo.f5.com and pulls the release manifest chart into cacheDir.
func PullReleaseManifest(_ context.Context, username, password, manifestVersion, cacheDir string) (*ReleaseManifest, error) {
	if manifestVersion == "" {
		manifestVersion = DefaultManifestVersion
	}
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir cache %s: %w", cacheDir, err)
	}
	absCache, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil, err
	}

	regClient, err := registry.NewClient(registry.ClientOptBasicAuth(username, password))
	if err != nil {
		return nil, fmt.Errorf("create helm registry client: %w", err)
	}

	extractedDir := filepath.Join(absCache, ReleaseManifestChart+"-"+manifestVersion)
	_ = os.RemoveAll(extractedDir)
	_ = os.RemoveAll(filepath.Join(absCache, ReleaseManifestChart))

	cfg := &action.Configuration{RegistryClient: regClient}
	pull := action.NewPullWithOpts(action.WithConfig(cfg))
	pull.Settings = cli.New()
	pull.Version = manifestVersion
	pull.DestDir = absCache
	pull.Untar = true
	pull.UntarDir = absCache
	if _, err := pull.Run(ReleaseManifestRepo + "/" + ReleaseManifestChart); err != nil {
		return nil, fmt.Errorf("pull release-manifest %s from %s: %w", manifestVersion, ReleaseManifestRepo, err)
	}
	if err := os.Rename(filepath.Join(absCache, ReleaseManifestChart), extractedDir); err != nil {
		return nil, fmt.Errorf("rename extracted chart: %w", err)
	}

	manifestPath, err := findManifestFile(extractedDir, manifestVersion)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(manifestPath) // #nosec G304 -- reading extracted manifest yaml
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}

	m, err := ParseReleaseManifest(body)
	if err != nil {
		return nil, err
	}

	_ = os.WriteFile(filepath.Join(absCache, "manifest.yaml"), body, 0o600) // #nosec G306,G703 -- cache write
	return m, nil
}

func findManifestFile(dir, manifestVersion string) (string, error) {
	exact := filepath.Join(dir, fmt.Sprintf("bigip-k8s-manifest-%s.yaml", manifestVersion))
	if _, err := os.Stat(exact); err == nil {
		return exact, nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, "bigip-k8s-manifest-*.yaml"))
	if err != nil {
		return "", fmt.Errorf("list manifests in %s: %w", dir, err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no bigip-k8s-manifest-*.yaml in %s (requested %s)", dir, manifestVersion)
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("%d release manifests in %s, none named for %s: %v", len(matches), dir, manifestVersion, matches)
	}
}

// PullAndInspect pulls oci://repo.f5.com/release/f5-bigip-k8s-manifest:<version>
// via the Helm v3 OCI client and extracts the paired FLO chart version and BOM.
func PullAndInspect(_ context.Context, version, saKeyJSON string) (*ReleaseManifest, error) {
	settings := cli.New()
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), "default", "memory", func(string, ...interface{}) {}); err != nil {
		return nil, fmt.Errorf("init helm config: %w", err)
	}

	regClient, err := registry.NewClient(
		registry.ClientOptDebug(false),
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(io.Discard),
	)
	if err != nil {
		return nil, fmt.Errorf("create helm registry client: %w", err)
	}

	if strings.TrimSpace(saKeyJSON) != "" {
		if err := regClient.Login(FARRegistryHost,
			registry.LoginOptBasicAuth("_json_key", saKeyJSON),
			registry.LoginOptInsecure(false),
		); err != nil {
			return nil, fmt.Errorf("login to repo.f5.com: %w", err)
		}
	}
	actionConfig.RegistryClient = regClient

	tmpDir, err := os.MkdirTemp("", "bnk-manifest-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	pullClient := action.NewPullWithOpts(action.WithConfig(actionConfig))
	pullClient.Settings = settings
	pullClient.DestDir = tmpDir
	pullClient.Version = strings.TrimPrefix(version, "v")

	chartRef := ReleaseManifestRepo + "/" + ReleaseManifestChart
	out, err := pullClient.Run(chartRef)
	if err != nil {
		return nil, fmt.Errorf("pulling %s:%s: %w (out: %s)", chartRef, pullClient.Version, err, out)
	}

	matches, err := filepath.Glob(filepath.Join(tmpDir, "*.tgz"))
	if err != nil || len(matches) == 0 {
		return nil, fmt.Errorf("locating pulled manifest archive in %s", tmpDir)
	}

	rm, err := parseTarGz(matches[0])
	if err != nil {
		return nil, err
	}
	if rm.Version == "" {
		rm.Version = pullClient.Version
	}
	if rm.FLOVersion == "" {
		if flo, ok := FLOChartFor(rm.Version); ok {
			rm.FLOVersion = flo
		}
	}
	return rm, nil
}

func parseTarGz(archivePath string) (*ReleaseManifest, error) {
	f, err := os.Open(archivePath) // #nosec G304 -- operator-supplied local tarball
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
	rm := &ReleaseManifest{
		HelmCharts: make(map[string]string),
		DockerImgs: make(map[string]string),
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		switch {
		case strings.HasSuffix(hdr.Name, "release-manifest.yaml") || strings.HasSuffix(hdr.Name, "manifest.yaml"):
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read manifest.yaml in tarball: %w", err)
			}
			parsed, err := ParseReleaseManifest(b)
			if err == nil {
				parsed.ChartNotes = rm.ChartNotes
				if parsed.FLOVersion == "" && rm.FLOVersion != "" {
					parsed.FLOVersion = rm.FLOVersion
				}
				rm = parsed
			} else {
				rm.RawManifest = b
				rm.RawManifestYAML = string(b)
			}
		case strings.HasSuffix(hdr.Name, "NOTES.txt"):
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			rm.ChartNotes = string(b)
			for _, line := range strings.Split(rm.ChartNotes, "\n") {
				if idx := strings.Index(line, "--version"); idx != -1 {
					fields := strings.Fields(line[idx:])
					if len(fields) >= 2 {
						rm.FLOVersion = strings.TrimSpace(fields[1])
					}
				}
			}
		}
	}

	if rm.RawManifestYAML == "" && len(rm.Components) == 0 && len(rm.HelmCharts) == 0 {
		return nil, fmt.Errorf("manifest.yaml not found inside archive %s", archivePath)
	}
	return rm, nil
}

// ProbeArchive attempts to extract and parse manifest.yaml or release-manifest.yaml from a given tarball or directory.
func ProbeArchive(path string) (*ReleaseManifest, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read path %s: %w", path, err)
	}

	if fi.IsDir() {
		for _, candidate := range []string{"manifest.yaml", "release-manifest.yaml"} {
			manifestFile := filepath.Join(path, candidate)
			if data, err := os.ReadFile(manifestFile); err == nil { // #nosec G304 -- path is operator-supplied local directory
				return ParseReleaseManifest(data)
			}
		}
		return nil, fmt.Errorf("manifest.yaml not found in %s", path)
	}

	return parseTarGz(path)
}

// GetEmbeddedManifest returns a built-in baseline manifest for inspection.
func GetEmbeddedManifest() *ReleaseManifest {
	flo, _ := FLOChartFor(DefaultManifestVersion)
	return &ReleaseManifest{
		HelmRepo:   "https://repo.f5.com/helm",
		DockerRepo: "repo.f5.com/f5-bnk",
		Version:    DefaultManifestVersion,
		FLOVersion: flo,
		HelmCharts: map[string]string{
			"f5-bigip-k8s-manifest": DefaultManifestVersion,
			"f5-lifecycle-operator": flo,
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

// Probe probes a manifest version against a local file/archive, live OCI registry (when a FAR auth key is provided),
// or returns the embedded baseline.
func Probe(ctx context.Context, version string, archivePath string) (*ReleaseManifest, error) {
	if archivePath != "" {
		if m, err := ProbeArchive(archivePath); err == nil {
			return m, nil
		}
		if user, pass, authErr := ExtractFARAuth(archivePath); authErr == nil && pass != "" {
			tmpCache, err := os.MkdirTemp("", "gkebnkctl-manifest-*")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(tmpCache)
			return PullReleaseManifest(ctx, user, pass, version, tmpCache)
		}
		return ProbeArchive(archivePath)
	}

	m := GetEmbeddedManifest()
	if version != "" && version != m.Version {
		m.Version = version
		m.HelmCharts["f5-bigip-k8s-manifest"] = version
		if flo, ok := FLOChartFor(version); ok {
			m.FLOVersion = flo
			m.HelmCharts["f5-lifecycle-operator"] = flo
		}
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
	{Version: "2.3.0-3.2598.3-0.0.171", FLOChart: "v2.21.13-0.0.28"},
	{Version: "2.3.1-3.2598.3-0.0.304", FLOChart: "v2.21.13-0.0.53"},
	{Version: "2.3.1-3.2598.3-0.0.312", FLOChart: "v2.21.13-0.0.53"},
	{Version: "2.3.2-3.2598.3-0.0.392", FLOChart: "v2.21.13-0.0.58"},
	{Version: "2.3.2-3.2598.3-0.0.464", FLOChart: "v2.21.13-0.0.58"},
	{Version: "2.3.3-3.2598.3-0.0.509", FLOChart: "v2.21.13-0.0.64"},
	{Version: "2.4.0", FLOChart: "v2.30.0-0.5.2"},
	{Version: "2.4.0-3.2882.0", FLOChart: "v2.30.0-0.5.2"},
	{Version: "2.5.0", FLOChart: "v2.40.0-0.1.0"},
}

// FLOChartFor returns the FLO chart version paired with manifestVersion.
func FLOChartFor(manifestVersion string) (chart string, ok bool) {
	v := strings.TrimPrefix(strings.TrimSpace(manifestVersion), "v")
	for _, r := range KnownReleases {
		if strings.TrimPrefix(r.Version, "v") == v {
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

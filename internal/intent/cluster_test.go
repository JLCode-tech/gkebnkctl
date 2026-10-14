package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validYAML returns a minimal-but-complete cluster.yaml body reproducing the
// bnk-hostdev trial values (docs/SPEC-go-tracer.md §2). jwtPath is filled in
// by the caller (writeCluster) to point at a real temp file, since
// bnk.jwtPath existence is checked at Load time.
const validYAML = `
apiVersion: gkebnkctl/v1
kind: Cluster

metadata:
  name: bnk-hostdev
  project: my-gcp-project
  zone: us-central1-a
  labels:
    purpose: bnk-hostdev-demo

pattern: host-device

network:
  main:
    vpc: bnkhd-vpc
    subnet: bnkhd-subnet
    cidr: 10.60.0.0/24
    podsRangeName: bnkhd-pods
    podsCidr: 10.66.0.0/16
    svcsRangeName: bnkhd-svcs
    svcsCidr: 10.67.0.0/20
  external:
    vpc: bnkhd-ext
    subnet: bnkhd-ext
    cidr: 10.30.0.0/24
    mtu: 8896
    podsRangeName: tmm-ext-pods
    podsCidr: 10.31.0.0/24
    underlayCidr: 10.30.0.0/16
  internal:
    vpc: bnkhd-int
    subnet: bnkhd-int
    cidr: 10.50.0.0/24
    mtu: 8896
    podsRangeName: tmm-int-pods
    podsCidr: 10.51.0.0/24
  allowInternalCidr: 10.0.0.0/8
  natGateways: true

cluster:
  kubernetesVersion: "1.35"
  defaultPool:
    machineType: e2-standard-4
    nodeCount: 3
  tmmPool:
    name: tmm-pool
    machineType: e2-standard-8
    nodeCount: 1
    maxPodsPerNode: 8
  agentPool:
    name: agent-pool
    machineType: e2-standard-4
    nodeCount: 1
    maxPodsPerNode: 8

bnk:
  manifestVersion: "2.4.0-3.2882.0"
  tmmMtu: "1460"
  jwtPath: __JWT_PATH__
  farSecret:
    source: kubecontext
    sourceContext: some-other-cluster
    sourceNamespace: f5-bnk
    namespaces: [f5-bnk, f5-utils]

egress:
  testNamespace: egress-test
  vxlanPort: 5789
  vxlanKey: 100
  vxlanMtu: 1410

tags:
  env: lab
  purpose: bnk-hostdev-demo
`

// writeCluster writes body (with __JWT_PATH__ substituted for a real temp
// file) to dir/cluster.yaml and returns its path. Per docs/SPEC-go-tracer.md
// §8 Task A note: the jwtPath existence check must be soft-fail-able in
// tests, so every test that needs a loadable cluster uses a real temp file.
func writeCluster(t *testing.T, dir, body string) string {
	t.Helper()
	jwtPath := filepath.Join(dir, "license.jwt")
	if err := os.WriteFile(jwtPath, []byte("DUMMY-NOT-A-REAL-JWT\n"), 0o600); err != nil {
		t.Fatalf("writing dummy jwt: %v", err)
	}
	body = strings.ReplaceAll(body, "__JWT_PATH__", jwtPath)
	path := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing cluster.yaml: %v", err)
	}
	return path
}

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeCluster(t, dir, validYAML)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if c.Metadata.Name != "bnk-hostdev" {
		t.Errorf("Metadata.Name = %q, want bnk-hostdev", c.Metadata.Name)
	}
	if c.Pattern != PatternHostDevice {
		t.Errorf("Pattern = %q, want %q", c.Pattern, PatternHostDevice)
	}
	if c.StateDir() != ".gkebnkctl/bnk-hostdev" {
		t.Errorf("StateDir() = %q, want .gkebnkctl/bnk-hostdev", c.StateDir())
	}
}

func TestLoad_StrictUnknownFieldRejection(t *testing.T) {
	dir := t.TempDir()
	body := validYAML + "\nunknownTopLevelField: true\n"
	path := writeCluster(t, dir, body)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown top-level field, got nil")
	}
}

func TestLoad_StrictUnknownFieldRejection_Nested(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(validYAML,
		"metadata:\n  name: bnk-hostdev",
		"metadata:\n  name: bnk-hostdev\n  bogusField: true",
		1)
	path := writeCluster(t, dir, body)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown nested field, got nil")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// TestValidate_EachRule exercises one validation failure at a time by
// mutating a single line of validYAML.
func TestValidate_EachRule(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{
			name: "bad apiVersion",
			mutate: func(y string) string {
				return strings.Replace(y, "apiVersion: gkebnkctl/v1", "apiVersion: gkebnkctl/v2", 1)
			},
			wantErr: "apiVersion",
		},
		{
			name: "bad kind",
			mutate: func(y string) string {
				return strings.Replace(y, "kind: Cluster", "kind: Pod", 1)
			},
			wantErr: "kind",
		},
		{
			name: "bad name regex (uppercase)",
			mutate: func(y string) string {
				return strings.Replace(y, "name: bnk-hostdev", "name: BNK-Hostdev", 1)
			},
			wantErr: "metadata.name",
		},
		{
			name: "bad name regex (too short)",
			mutate: func(y string) string {
				return strings.Replace(y, "name: bnk-hostdev", "name: a", 1)
			},
			wantErr: "metadata.name",
		},
		{
			name: "missing project",
			mutate: func(y string) string {
				return strings.Replace(y, "project: my-gcp-project", "project: \"\"", 1)
			},
			wantErr: "metadata.project",
		},
		{
			name: "missing zone",
			mutate: func(y string) string {
				return strings.Replace(y, "zone: us-central1-a", "zone: \"\"", 1)
			},
			wantErr: "metadata.zone",
		},
		{
			name: "unsupported pattern",
			mutate: func(y string) string {
				return strings.Replace(y, "pattern: host-device", "pattern: dual-interface", 1)
			},
			wantErr: "pattern",
		},
		{
			name: "malformed main cidr",
			mutate: func(y string) string {
				return strings.Replace(y, "cidr: 10.60.0.0/24", "cidr: not-a-cidr", 1)
			},
			wantErr: "network.main.cidr",
		},
		{
			name: "missing external vpc",
			mutate: func(y string) string {
				return strings.Replace(y, "vpc: bnkhd-ext", "vpc: \"\"", 1)
			},
			wantErr: "network.external.vpc",
		},
		{
			name: "missing manifestVersion",
			mutate: func(y string) string {
				return strings.Replace(y, `manifestVersion: "2.4.0-3.2882.0"`, `manifestVersion: ""`, 1)
			},
			wantErr: "bnk.manifestVersion",
		},
		{
			name: "farSecret bad enum",
			mutate: func(y string) string {
				return strings.Replace(y, "source: kubecontext", "source: bogus", 1)
			},
			wantErr: "bnk.farSecret.source",
		},
		{
			name: "farSecret kubecontext missing sourceContext",
			mutate: func(y string) string {
				return strings.Replace(y, "sourceContext: some-other-cluster", "sourceContext: \"\"", 1)
			},
			wantErr: "bnk.farSecret.sourceContext",
		},
		{
			name: "negative external mtu",
			mutate: func(y string) string {
				return strings.Replace(y, "mtu: 8896", "mtu: -1", 1)
			},
			wantErr: "network.external.mtu",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeCluster(t, dir, tt.mutate(validYAML))
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidate_JwtPathMustExist(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(validYAML, "jwtPath: __JWT_PATH__", "jwtPath: ./nonexistent-license.jwt", 1)
	// Bypass writeCluster's auto temp-file substitution for this one case.
	path := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing cluster.yaml: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing jwtPath file, got nil")
	}
	if !strings.Contains(err.Error(), "jwtPath") {
		t.Errorf("error = %q, want substring \"jwtPath\"", err.Error())
	}
}

func TestValidate_FarSecretDockerConfigRequiresFile(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(validYAML,
		"farSecret:\n    source: kubecontext\n    sourceContext: some-other-cluster\n    sourceNamespace: f5-bnk\n    namespaces: [f5-bnk, f5-utils]",
		"farSecret:\n    source: dockerconfig\n    sourceNamespace: f5-bnk\n    namespaces: [f5-bnk, f5-utils]",
		1)
	path := writeCluster(t, dir, body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for dockerconfig source without dockerConfigFile, got nil")
	}
	if !strings.Contains(err.Error(), "dockerConfigFile") {
		t.Errorf("error = %q, want substring \"dockerConfigFile\"", err.Error())
	}
}

func TestLoad_DockerConfigFileMustExist(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(validYAML,
		"farSecret:\n    source: kubecontext\n    sourceContext: some-other-cluster\n    sourceNamespace: f5-bnk\n    namespaces: [f5-bnk, f5-utils]",
		"farSecret:\n    source: dockerconfig\n    dockerConfigFile: ./nonexistent.json\n    sourceNamespace: f5-bnk\n    namespaces: [f5-bnk, f5-utils]",
		1)
	path := writeCluster(t, dir, body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing dockerConfigFile, got nil")
	}
	if !strings.Contains(err.Error(), "dockerConfigFile") {
		t.Errorf("error = %q, want substring \"dockerConfigFile\"", err.Error())
	}
}

func TestApplyDefaults(t *testing.T) {
	dir := t.TempDir()
	// Minimal YAML that omits every field with a documented default.
	minimal := `
apiVersion: gkebnkctl/v1
kind: Cluster
metadata:
  name: bnk-hostdev
  project: my-gcp-project
  zone: us-central1-a
pattern: host-device
network:
  main:
    vpc: bnkhd-vpc
    subnet: bnkhd-subnet
    cidr: 10.60.0.0/24
    podsRangeName: bnkhd-pods
    podsCidr: 10.66.0.0/16
    svcsRangeName: bnkhd-svcs
    svcsCidr: 10.67.0.0/20
  external:
    vpc: bnkhd-ext
    subnet: bnkhd-ext
    cidr: 10.30.0.0/24
    mtu: 8896
    podsRangeName: tmm-ext-pods
    podsCidr: 10.31.0.0/24
    underlayCidr: 10.30.0.0/16
  internal:
    vpc: bnkhd-int
    subnet: bnkhd-int
    cidr: 10.50.0.0/24
    mtu: 8896
    podsRangeName: tmm-int-pods
    podsCidr: 10.51.0.0/24
  allowInternalCidr: 10.0.0.0/8
bnk:
  manifestVersion: "2.4.0-3.2882.0"
  jwtPath: __JWT_PATH__
  farSecret:
    source: kubecontext
    sourceContext: some-other-cluster
`
	path := writeCluster(t, dir, minimal)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}

	if !c.Network.NatGatewaysEnabled() {
		t.Error("NatGatewaysEnabled() = false, want true (default)")
	}
	if c.ClusterSpec.KubernetesVersion != "1.35" {
		t.Errorf("ClusterSpec.KubernetesVersion = %q, want 1.35", c.ClusterSpec.KubernetesVersion)
	}
	if c.ClusterSpec.DefaultPool.MachineType != "e2-standard-4" || c.ClusterSpec.DefaultPool.NodeCount != 3 {
		t.Errorf("DefaultPool = %+v, want machineType=e2-standard-4 nodeCount=3", c.ClusterSpec.DefaultPool)
	}
	if c.ClusterSpec.TmmPool.Name != "tmm-pool" || c.ClusterSpec.TmmPool.MachineType != "e2-standard-8" ||
		c.ClusterSpec.TmmPool.NodeCount != 1 || c.ClusterSpec.TmmPool.MaxPodsPerNode != 8 {
		t.Errorf("TmmPool = %+v, want name=tmm-pool machineType=e2-standard-8 nodeCount=1 maxPodsPerNode=8", c.ClusterSpec.TmmPool)
	}
	if c.ClusterSpec.AgentPool.Name != "agent-pool" {
		t.Errorf("AgentPool.Name = %q, want agent-pool", c.ClusterSpec.AgentPool.Name)
	}
	if c.Bnk.TmmMtu != "1460" {
		t.Errorf("Bnk.TmmMtu = %q, want 1460", c.Bnk.TmmMtu)
	}
	if c.Bnk.ImageRegistry != "us-west1-docker.pkg.dev/f5-gcs-7056-ptg-cne-prod/spk-repo-non-ga" {
		t.Errorf("Bnk.ImageRegistry = %q, want default GAR registry", c.Bnk.ImageRegistry)
	}
	if c.Bnk.FarSecret.SourceNamespace != "f5-bnk" {
		t.Errorf("FarSecret.SourceNamespace = %q, want f5-bnk", c.Bnk.FarSecret.SourceNamespace)
	}
	wantNS := []string{"f5-bnk", "f5-utils"}
	if len(c.Bnk.FarSecret.Namespaces) != len(wantNS) {
		t.Fatalf("FarSecret.Namespaces = %v, want %v", c.Bnk.FarSecret.Namespaces, wantNS)
	}
	for i, ns := range wantNS {
		if c.Bnk.FarSecret.Namespaces[i] != ns {
			t.Errorf("FarSecret.Namespaces[%d] = %q, want %q", i, c.Bnk.FarSecret.Namespaces[i], ns)
		}
	}
	if c.Egress.TestNamespace != "egress-test" || c.Egress.VxlanPort != 5789 ||
		c.Egress.VxlanKey != 100 || c.Egress.VxlanMtu != 1410 {
		t.Errorf("Egress = %+v, want the documented defaults", c.Egress)
	}
}

func TestNatGatewaysEnabled_ExplicitFalse(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(validYAML, "natGateways: true", "natGateways: false", 1)
	path := writeCluster(t, dir, body)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if c.Network.NatGatewaysEnabled() {
		t.Error("NatGatewaysEnabled() = true, want false (explicit override)")
	}
}

func TestDerivedValues(t *testing.T) {
	dir := t.TempDir()
	path := writeCluster(t, dir, validYAML)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}

	if c.Region != "us-central1" {
		t.Errorf("Region = %q, want us-central1", c.Region)
	}
	if c.ExtGwIP != "10.30.0.1" {
		t.Errorf("ExtGwIP = %q, want 10.30.0.1", c.ExtGwIP)
	}
	if c.IntGwIP != "10.50.0.1" {
		t.Errorf("IntGwIP = %q, want 10.50.0.1", c.IntGwIP)
	}
	if c.ExtUnderlayDest != "10.30.0.0" || c.ExtUnderlayPrefixLen != 16 {
		t.Errorf("ExtUnderlay = %s/%d, want 10.30.0.0/16", c.ExtUnderlayDest, c.ExtUnderlayPrefixLen)
	}
	if c.MainPodsDest != "10.66.0.0" || c.MainPodsPrefixLen != 16 {
		t.Errorf("MainPods = %s/%d, want 10.66.0.0/16", c.MainPodsDest, c.MainPodsPrefixLen)
	}
	if c.MainSvcsDest != "10.67.0.0" || c.MainSvcsPrefixLen != 20 {
		t.Errorf("MainSvcs = %s/%d, want 10.67.0.0/20", c.MainSvcsDest, c.MainSvcsPrefixLen)
	}
}

func TestPatternMustBeHostDevice(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"external-only", "dual-interface", "", "sriov-external"} {
		body := strings.Replace(validYAML, "pattern: host-device", "pattern: "+quoteIfEmpty(p), 1)
		path := writeCluster(t, dir, body)
		if _, err := Load(path); err == nil {
			t.Errorf("pattern %q: expected error, got nil", p)
		}
	}
}

func quoteIfEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

func TestDefaultVIP(t *testing.T) {
	cases := []struct {
		cidr     string
		podsCidr string
		want     string
		wantErr  bool
	}{
		{"10.31.0.0/24", "", "10.31.0.100", false},
		{"192.168.1.0/24", "", "192.168.1.100", false},
		{"10.81.0.0/24", "10.81.1.0/24", "10.81.1.10", false},
		{"", "", "", true},
		{"bad-cidr", "", "", true},
	}
	for _, tc := range cases {
		c := &Cluster{Network: Network{External: DataplaneSpec{CIDR: tc.cidr, PodsCIDR: tc.podsCidr}}}
		got, err := c.DefaultVIP()
		if (err != nil) != tc.wantErr {
			t.Errorf("DefaultVIP(%q, %q) err = %v, wantErr = %v", tc.cidr, tc.podsCidr, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("DefaultVIP(%q, %q) = %q, want %q", tc.cidr, tc.podsCidr, got, tc.want)
		}
	}
}

func TestForgeSpecResolvers(t *testing.T) {
	var f *ForgeSpec
	if got := f.ResolveURL(); got != DefaultForgeRESTURL {
		t.Errorf("nil ForgeSpec ResolveURL() = %q, want %q", got, DefaultForgeRESTURL)
	}
	if got := f.ResolveUsername(); got != "admin" {
		t.Errorf("nil ForgeSpec ResolveUsername() = %q, want admin", got)
	}
	if got, def := f.ResolvePassword(); got != "changeme" || !def {
		t.Errorf("nil ForgeSpec ResolvePassword() = %q, %v, want changeme, true", got, def)
	}
	if got := f.ResolveEnvironment(); got != "dev" {
		t.Errorf("nil ForgeSpec ResolveEnvironment() = %q, want dev", got)
	}
	if got := f.ResolveProjectType(); got != "cloud-gcp" {
		t.Errorf("nil ForgeSpec ResolveProjectType() = %q, want cloud-gcp", got)
	}

	f = &ForgeSpec{
		URL:         "http://forge.example.com:8000",
		Username:    "custom-user",
		Password:    "secret",
		Environment: "prod",
		ProjectType: "cloud-gcp-custom",
	}
	if got := f.ResolveURL(); got != "http://forge.example.com:8000" {
		t.Errorf("ResolveURL() = %q", got)
	}
	if got := f.ResolveUsername(); got != "custom-user" {
		t.Errorf("ResolveUsername() = %q", got)
	}
	if got, def := f.ResolvePassword(); got != "secret" || def {
		t.Errorf("ResolvePassword() = %q, %v", got, def)
	}
	if got := f.ResolveEnvironment(); got != "prod" {
		t.Errorf("ResolveEnvironment() = %q", got)
	}
	if got := f.ResolveProjectType(); got != "cloud-gcp-custom" {
		t.Errorf("ResolveProjectType() = %q", got)
	}
}

func TestSyntheticAIEnabled(t *testing.T) {
	var c Cluster
	if c.SyntheticAIEnabled() {
		t.Error("empty cluster SyntheticAIEnabled() = true, want false")
	}
	c.AI = &AISpec{Synthetic: &SyntheticSpec{Enabled: true, ServedModelName: "llama3"}}
	if !c.SyntheticAIEnabled() {
		t.Error("SyntheticAIEnabled() = false, want true")
	}
}

// Package intent holds the cluster.yaml schema (gkebnkctl/v1) and loader.
//
// The canonical format is described in docs/SPEC-go-tracer.md §2. Every
// field maps directly to a CFG_*/DERIVED_* variable the bash
// reference/bash-seed/lib/config.sh + manifests/*.tmpl consume — the doc
// comments below cross-reference the bash-seed equivalent so this schema can
// be checked against the live-proven recipe.
package intent

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JLCode-tech/gkebnkctl/internal/netcalc"
)

// nameRE enforces the GKE cluster name rules used as the metadata.name
// pattern: lowercase alphanumeric + hyphens, 2-40 chars, must start with a
// letter and end with a letter or digit.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,38}[a-z0-9]$`)

// PatternHostDevice is the only pattern this seed supports. Kept as a named
// constant (rather than a bare literal) for forward-compat with awsbnkctl's
// pattern: field, which supports several values.
const PatternHostDevice = "host-device"

// APIVersion and Kind are the only accepted values for their respective
// cluster.yaml fields.
const (
	APIVersion = "gkebnkctl/v1"
	Kind       = "Cluster"
)

// Cluster is the Go representation of cluster.yaml (apiVersion: gkebnkctl/v1,
// kind: Cluster). Unknown fields are rejected at load time (decodeStrict).
type Cluster struct {
	APIVersion  string            `yaml:"apiVersion"`
	Kind        string            `yaml:"kind"`
	Metadata    Metadata          `yaml:"metadata"`
	Pattern     string            `yaml:"pattern"`
	Network     Network           `yaml:"network"`
	ClusterSpec ClusterSpec       `yaml:"cluster,omitempty"`
	Bnk         BnkSpec           `yaml:"bnk"`
	Egress      EgressSpec        `yaml:"egress,omitempty"`
	Tags        map[string]string `yaml:"tags,omitempty"`

	// --- Derived fields. Never operator-set; computed after validate()
	// succeeds (see Load). yaml:"-" excludes them from both decode and
	// encode so they can never leak in from cluster.yaml. ---

	// Region is zone with its trailing "-<letter>" suffix stripped, e.g.
	// "us-central1-a" -> "us-central1". Bash equivalent: DERIVED_REGION.
	Region string `yaml:"-"`
	// ExtGwIP is netcalc.Nth(network.external.cidr, 1) — the external
	// dataplane default-gateway convention. Bash equivalent: DERIVED_EXT_GW_IP.
	ExtGwIP string `yaml:"-"`
	// IntGwIP is netcalc.Nth(network.internal.cidr, 1) — the internal
	// dataplane default-egress gateway. Bash equivalent: DERIVED_INT_GW_IP.
	IntGwIP string `yaml:"-"`
	// ExtUnderlayDest/ExtUnderlayPrefixLen split network.external.underlayCidr
	// for the route-ext-underlay F5SPKStaticRoute template.
	ExtUnderlayDest      string `yaml:"-"`
	ExtUnderlayPrefixLen int    `yaml:"-"`
	// MainPodsDest/MainPodsPrefixLen split network.main.podsCidr for the
	// cluster-pods F5SPKStaticRoute template.
	MainPodsDest      string `yaml:"-"`
	MainPodsPrefixLen int    `yaml:"-"`
	// MainSvcsDest/MainSvcsPrefixLen split network.main.svcsCidr for the
	// cluster-svcs F5SPKStaticRoute template.
	MainSvcsDest      string `yaml:"-"`
	MainSvcsPrefixLen int    `yaml:"-"`
}

// Metadata carries the cluster identity fields.
type Metadata struct {
	// Name is load-bearing: it becomes the GKE cluster name and the state
	// directory name under .gkebnkctl/. Must match nameRE.
	Name    string            `yaml:"name"`
	Project string            `yaml:"project"`
	Zone    string            `yaml:"zone"`
	Labels  map[string]string `yaml:"labels,omitempty"`
}

// Network describes the three VPCs the provisioner creates/attaches:
// main (cluster-owned), external + internal (shared dataplane, never
// destroyed by teardown).
type Network struct {
	Main     MainNetworkSpec `yaml:"main"`
	External DataplaneSpec   `yaml:"external"`
	Internal DataplaneSpec   `yaml:"internal"`
	// AllowInternalCidr sources the allow-internal firewall rule on the main
	// VPC.
	AllowInternalCidr string `yaml:"allowInternalCidr"`
	// NatGateways controls whether Cloud Router+NAT are created on the
	// external+internal VPCs. Pointer so "omitted" (default true) is
	// distinguishable from an explicit "false".
	NatGateways *bool `yaml:"natGateways,omitempty"`
}

// NatGatewaysEnabled reports whether Cloud Router+NAT should be created.
// Default true when the field is omitted from cluster.yaml.
func (n Network) NatGatewaysEnabled() bool {
	if n.NatGateways == nil {
		return true
	}
	return *n.NatGateways
}

// MainNetworkSpec is the cluster-OWNED VPC — created and destroyed with the
// cluster (bash: bin/10-networks.sh + bin/90-teardown.sh main-vpc branch).
type MainNetworkSpec struct {
	VPC           string `yaml:"vpc"`
	Subnet        string `yaml:"subnet"`
	CIDR          string `yaml:"cidr"`
	PodsRangeName string `yaml:"podsRangeName"`
	PodsCIDR      string `yaml:"podsCidr"`
	SvcsRangeName string `yaml:"svcsRangeName"`
	SvcsCIDR      string `yaml:"svcsCidr"`
}

// DataplaneSpec is a SHARED dataplane VPC (external or internal) —
// created-if-missing, NEVER destroyed by teardown (bash: bin/10-networks.sh
// treats these as potentially-shared infrastructure).
//
// UnderlayCIDR is only meaningful (and required) for network.external; it is
// left empty for network.internal.
type DataplaneSpec struct {
	VPC           string `yaml:"vpc"`
	Subnet        string `yaml:"subnet"`
	CIDR          string `yaml:"cidr"`
	MTU           int    `yaml:"mtu"`
	PodsRangeName string `yaml:"podsRangeName"`
	PodsCIDR      string `yaml:"podsCidr"`
	// UnderlayCIDR is the broader summary route for the shared ext underlay
	// (gcloud routers/NAT and F5SPKStaticRoute route-ext-underlay both use
	// this). Required on network.external only.
	UnderlayCIDR string `yaml:"underlayCidr,omitempty"`
}

// ClusterSpec holds the GKE cluster + node pool configuration.
// Corresponds to the `cluster:` block in cluster.yaml.
type ClusterSpec struct {
	// KubernetesVersion is the GKE minor version. Default "1.31".
	KubernetesVersion string `yaml:"kubernetesVersion,omitempty"`
	// DefaultPool is the standard (non-TMM, non-agent) node pool.
	DefaultPool NodePoolSpec `yaml:"defaultPool,omitempty"`
	// TmmPool carries the TMM data-plane workload (host-device NADs, PCI
	// passthrough). Gets f5-dataplane=true + 2 additional-node-networks + 2
	// additional-pod-networks (Phase 20).
	TmmPool NodePoolSpec `yaml:"tmmPool,omitempty"`
	// AgentPool runs the egress-agent DaemonSet + test client (Phase 50).
	AgentPool NodePoolSpec `yaml:"agentPool,omitempty"`
}

// NodePoolSpec configures one GKE node pool.
type NodePoolSpec struct {
	// Name is the node pool name. Required (with a default) for tmmPool and
	// agentPool; defaultPool has no Name field in cluster.yaml (GKE assigns
	// "default-pool").
	Name string `yaml:"name,omitempty"`
	// MachineType is the GCE machine type, e.g. "e2-standard-4".
	MachineType string `yaml:"machineType,omitempty"`
	// NodeCount is the fixed node count (no autoscaling in the tracer).
	NodeCount int `yaml:"nodeCount,omitempty"`
	// MaxPodsPerNode maps to --additional-pod-network max-pods-per-node.
	// Meaningful only for tmmPool/agentPool (multi-networking pools).
	MaxPodsPerNode int `yaml:"maxPodsPerNode,omitempty"`
}

// BnkSpec holds the BNK install configuration.
type BnkSpec struct {
	// ManifestVersion is the BNK manifest version pulled by FLO. Required.
	ManifestVersion string `yaml:"manifestVersion"`
	// TmmMtu is the TMM interface MTU (F5SPKVlan.mtu / CNEInstance
	// TMM_DEFAULT_MTU). Distinct from network.{external,internal}.mtu (the
	// VPC wire/jumbo-frame MTU, 8896) — do not conflate the two. Default
	// "1460".
	TmmMtu string `yaml:"tmmMtu,omitempty"`
	// JwtPath is the path to the F5 subscription JWT. Runtime input,
	// gitignored, never embedded. Resolved relative to the cluster.yaml
	// file's directory when relative (see Load). Required.
	JwtPath string `yaml:"jwtPath"`
	// FarSecret configures how the far-secret pull credential is sourced.
	FarSecret FarSecretSpec `yaml:"farSecret,omitempty"`
}

// FarSecretSpec configures the far-secret pull-credential source.
type FarSecretSpec struct {
	// Source selects the far-secret provisioning mode: "kubecontext" (copy
	// from another cluster you have kubectl access to) or "dockerconfig"
	// (build from a local docker-config JSON). Required.
	Source string `yaml:"source"`
	// SourceContext is the kube context to copy far-secret FROM. Required
	// iff source == "kubecontext".
	SourceContext string `yaml:"sourceContext,omitempty"`
	// SourceNamespace is the source namespace for the copy. Default "f5-bnk".
	SourceNamespace string `yaml:"sourceNamespace,omitempty"`
	// DockerConfigFile is the path to a docker-config JSON. Runtime input,
	// gitignored. Required iff source == "dockerconfig".
	DockerConfigFile string `yaml:"dockerConfigFile,omitempty"`
	// Namespaces are the namespaces to install far-secret into. Default
	// [f5-bnk, f5-utils].
	Namespaces []string `yaml:"namespaces,omitempty"`
}

// EgressSpec configures the Phase50 egress test scaffold.
type EgressSpec struct {
	TestNamespace string `yaml:"testNamespace,omitempty"`
	VxlanPort     int    `yaml:"vxlanPort,omitempty"`
	VxlanKey      int    `yaml:"vxlanKey,omitempty"`
	VxlanMtu      int    `yaml:"vxlanMtu,omitempty"`
}

// StateDir returns the path to the IDs-cache directory for this cluster
// relative to the caller's working directory. Callers that need an absolute
// path should use filepath.Abs on the result.
func (c *Cluster) StateDir() string {
	return ".gkebnkctl/" + c.Metadata.Name
}

// Load reads and validates a cluster.yaml file at path.
//
// Pipeline: decodeStrict -> applyDefaults -> validate -> deriveComputed.
// Derived values are computed only after validate() succeeds, so a malformed
// CIDR produces validate()'s clear error message rather than a silently
// empty derived field.
func Load(path string) (*Cluster, error) {
	// #nosec G304 -- path is operator-supplied via the `-f`/positional flag;
	// gkebnkctl is a CLI tool so reading a user-named config file is
	// intentional behaviour, not a directory-traversal risk.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading cluster.yaml %s: %w", path, err)
	}

	var c Cluster
	if err := decodeStrict(data, &c); err != nil {
		return nil, fmt.Errorf("parsing cluster.yaml %s: %w", path, err)
	}

	baseDir := filepath.Dir(path)
	applyDefaults(&c, baseDir)
	if err := validate(&c); err != nil {
		return nil, fmt.Errorf("validating cluster.yaml %s: %w", path, err)
	}
	if err := deriveComputed(&c); err != nil {
		// Should be unreachable — validate() already proved every CIDR
		// referenced here parses. Surfaced defensively rather than panicking.
		return nil, fmt.Errorf("deriving cluster.yaml %s: %w", path, err)
	}
	return &c, nil
}

// decodeStrict decodes YAML rejecting unknown fields.
func decodeStrict(data []byte, out interface{}) error {
	dec := yaml.NewDecoder(bytesReader(data))
	dec.KnownFields(true)
	return dec.Decode(out)
}

// byteReader wraps a byte slice in an io.Reader for yaml.NewDecoder.
type byteReader struct {
	data []byte
	pos  int
}

func bytesReader(data []byte) *byteReader { return &byteReader{data: data} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// applyDefaults fills in zero-value fields with their documented defaults.
// Called before validate so validation sees the post-default values.
//
// baseDir is the directory containing the cluster.yaml file being loaded;
// relative bnk.jwtPath / bnk.farSecret.dockerConfigFile values are resolved
// against it so the file-existence check (validate) and any later phase
// reading these paths behave the same regardless of the caller's CWD.
func applyDefaults(c *Cluster, baseDir string) {
	// network.{external,internal}.mtu has no default — 8896 is documented in
	// the example but operator-set; a zero value is left for validate() to
	// reject as a missing required field.

	if c.ClusterSpec.KubernetesVersion == "" {
		c.ClusterSpec.KubernetesVersion = "1.31"
	}
	applyPoolDefaults(&c.ClusterSpec.DefaultPool, "", "e2-standard-4", 3, 0)
	applyPoolDefaults(&c.ClusterSpec.TmmPool, "tmm-pool", "e2-standard-8", 1, 8)
	applyPoolDefaults(&c.ClusterSpec.AgentPool, "agent-pool", "e2-standard-4", 1, 8)

	if c.Bnk.TmmMtu == "" {
		c.Bnk.TmmMtu = "1460"
	}
	if c.Bnk.JwtPath != "" && !filepath.IsAbs(c.Bnk.JwtPath) {
		c.Bnk.JwtPath = filepath.Join(baseDir, c.Bnk.JwtPath)
	}
	if c.Bnk.FarSecret.SourceNamespace == "" {
		c.Bnk.FarSecret.SourceNamespace = "f5-bnk"
	}
	if len(c.Bnk.FarSecret.Namespaces) == 0 {
		c.Bnk.FarSecret.Namespaces = []string{"f5-bnk", "f5-utils"}
	}
	if c.Bnk.FarSecret.DockerConfigFile != "" && !filepath.IsAbs(c.Bnk.FarSecret.DockerConfigFile) {
		c.Bnk.FarSecret.DockerConfigFile = filepath.Join(baseDir, c.Bnk.FarSecret.DockerConfigFile)
	}

	if c.Egress.TestNamespace == "" {
		c.Egress.TestNamespace = "egress-test"
	}
	if c.Egress.VxlanPort == 0 {
		c.Egress.VxlanPort = 5789
	}
	if c.Egress.VxlanKey == 0 {
		c.Egress.VxlanKey = 100
	}
	if c.Egress.VxlanMtu == 0 {
		c.Egress.VxlanMtu = 1410
	}
}

// applyPoolDefaults fills zero-value NodePoolSpec fields. name is only
// applied when the pool's Name field is empty AND defaultName is non-empty
// (defaultPool has no default name — GKE assigns "default-pool").
func applyPoolDefaults(p *NodePoolSpec, defaultName, defaultMachineType string, defaultNodeCount, defaultMaxPods int) {
	if p.Name == "" && defaultName != "" {
		p.Name = defaultName
	}
	if p.MachineType == "" {
		p.MachineType = defaultMachineType
	}
	if p.NodeCount == 0 {
		p.NodeCount = defaultNodeCount
	}
	if p.MaxPodsPerNode == 0 && defaultMaxPods != 0 {
		p.MaxPodsPerNode = defaultMaxPods
	}
}

// deriveComputed fills the Cluster's derived fields (Region, gateway IPs,
// route CIDR splits). Called by Load only after validate() has already
// proven every CIDR referenced here parses.
func deriveComputed(c *Cluster) error {
	c.Region = deriveRegion(c.Metadata.Zone)

	var err error
	if c.ExtGwIP, err = netcalc.Nth(c.Network.External.CIDR, 1); err != nil {
		return fmt.Errorf("network.external.cidr: %w", err)
	}
	if c.IntGwIP, err = netcalc.Nth(c.Network.Internal.CIDR, 1); err != nil {
		return fmt.Errorf("network.internal.cidr: %w", err)
	}
	if c.ExtUnderlayDest, c.ExtUnderlayPrefixLen, err = splitCIDR(c.Network.External.UnderlayCIDR); err != nil {
		return fmt.Errorf("network.external.underlayCidr: %w", err)
	}
	if c.MainPodsDest, c.MainPodsPrefixLen, err = splitCIDR(c.Network.Main.PodsCIDR); err != nil {
		return fmt.Errorf("network.main.podsCidr: %w", err)
	}
	if c.MainSvcsDest, c.MainSvcsPrefixLen, err = splitCIDR(c.Network.Main.SvcsCIDR); err != nil {
		return fmt.Errorf("network.main.svcsCidr: %w", err)
	}
	return nil
}

// deriveRegion strips the trailing "-<zoneLetter>" segment off a GCP zone,
// e.g. "us-central1-a" -> "us-central1". Bash equivalent: DERIVED_REGION
// (${CFG_METADATA_ZONE%-*}).
func deriveRegion(zone string) string {
	idx := strings.LastIndex(zone, "-")
	if idx < 0 {
		return zone
	}
	return zone[:idx]
}

// splitCIDR returns the network-address + prefix-length halves of cidr via
// netcalc, for route-template rendering.
func splitCIDR(cidr string) (addr string, prefixLen int, err error) {
	addr, err = netcalc.CIDRAddr(cidr)
	if err != nil {
		return "", 0, err
	}
	prefixLen, err = netcalc.CIDRPrefixLen(cidr)
	if err != nil {
		return "", 0, err
	}
	return addr, prefixLen, nil
}

// validate checks semantic constraints on the loaded cluster. Mirrors
// docs/SPEC-go-tracer.md §2 "Validation rules".
func validate(c *Cluster) error {
	if c.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q, got %q", APIVersion, c.APIVersion)
	}
	if c.Kind != Kind {
		return fmt.Errorf("kind must be %q, got %q", Kind, c.Kind)
	}
	if !nameRE.MatchString(c.Metadata.Name) {
		return fmt.Errorf("metadata.name %q does not match required pattern %s", c.Metadata.Name, nameRE.String())
	}
	if c.Metadata.Project == "" {
		return errors.New("metadata.project is required")
	}
	if c.Metadata.Zone == "" {
		return errors.New("metadata.zone is required")
	}
	if c.Pattern != PatternHostDevice {
		return fmt.Errorf("pattern must be %q (the only supported value), got %q", PatternHostDevice, c.Pattern)
	}

	if err := validateNetwork(c); err != nil {
		return err
	}
	if c.Bnk.ManifestVersion == "" {
		return errors.New("bnk.manifestVersion is required")
	}
	if c.Bnk.JwtPath == "" {
		return errors.New("bnk.jwtPath is required")
	}
	if _, err := os.Stat(c.Bnk.JwtPath); err != nil {
		return fmt.Errorf("bnk.jwtPath %q: %w", c.Bnk.JwtPath, err)
	}
	if err := validateFarSecret(c.Bnk.FarSecret); err != nil {
		return err
	}

	warnCIDROverlaps(c)
	return nil
}

// cidrField pairs a cluster.yaml field path with its value, for the
// required-CIDR validation loop.
type cidrField struct {
	path  string
	value string
}

// validateNetwork checks every required network field is set and every CIDR
// parses.
func validateNetwork(c *Cluster) error {
	required := []struct {
		path  string
		value string
	}{
		{"network.main.vpc", c.Network.Main.VPC},
		{"network.main.subnet", c.Network.Main.Subnet},
		{"network.main.podsRangeName", c.Network.Main.PodsRangeName},
		{"network.main.svcsRangeName", c.Network.Main.SvcsRangeName},
		{"network.external.vpc", c.Network.External.VPC},
		{"network.external.subnet", c.Network.External.Subnet},
		{"network.external.podsRangeName", c.Network.External.PodsRangeName},
		{"network.internal.vpc", c.Network.Internal.VPC},
		{"network.internal.subnet", c.Network.Internal.Subnet},
		{"network.internal.podsRangeName", c.Network.Internal.PodsRangeName},
		{"network.allowInternalCidr", c.Network.AllowInternalCidr},
	}
	for _, f := range required {
		if f.value == "" {
			return fmt.Errorf("%s is required", f.path)
		}
	}
	if c.Network.External.MTU <= 0 {
		return errors.New("network.external.mtu is required and must be positive")
	}
	if c.Network.Internal.MTU <= 0 {
		return errors.New("network.internal.mtu is required and must be positive")
	}

	cidrs := []cidrField{
		{"network.main.cidr", c.Network.Main.CIDR},
		{"network.main.podsCidr", c.Network.Main.PodsCIDR},
		{"network.main.svcsCidr", c.Network.Main.SvcsCIDR},
		{"network.external.cidr", c.Network.External.CIDR},
		{"network.external.podsCidr", c.Network.External.PodsCIDR},
		{"network.external.underlayCidr", c.Network.External.UnderlayCIDR},
		{"network.internal.cidr", c.Network.Internal.CIDR},
		{"network.internal.podsCidr", c.Network.Internal.PodsCIDR},
		{"network.allowInternalCidr", c.Network.AllowInternalCidr},
	}
	for _, f := range cidrs {
		if f.value == "" {
			return fmt.Errorf("%s is required", f.path)
		}
		if _, _, err := net.ParseCIDR(f.value); err != nil {
			return fmt.Errorf("%s %q does not parse: %w", f.path, f.value, err)
		}
	}
	return nil
}

// validateFarSecret enforces the source enum + conditionally-required
// companion field.
func validateFarSecret(f FarSecretSpec) error {
	switch f.Source {
	case "kubecontext":
		if f.SourceContext == "" {
			return errors.New("bnk.farSecret.sourceContext is required when bnk.farSecret.source == \"kubecontext\"")
		}
	case "dockerconfig":
		if f.DockerConfigFile == "" {
			return errors.New("bnk.farSecret.dockerConfigFile is required when bnk.farSecret.source == \"dockerconfig\"")
		}
	default:
		return fmt.Errorf("bnk.farSecret.source must be one of {kubecontext, dockerconfig}, got %q", f.Source)
	}
	return nil
}

// warnCIDROverlaps prints a non-fatal warning to stderr when the main
// network's primary CIDR range obviously overlaps an external/internal
// dataplane primary range. CIDR sanity is warn-only per spec §2 — a real
// overlap is an operator misconfiguration but not a hard-fail here, since
// the three networks are independent VPCs (no inherent routing conflict at
// validate time).
func warnCIDROverlaps(c *Cluster) {
	type named struct {
		label string
		cidr  string
	}
	nets := []named{
		{"network.main.cidr", c.Network.Main.CIDR},
		{"network.external.cidr", c.Network.External.CIDR},
		{"network.internal.cidr", c.Network.Internal.CIDR},
	}
	for i := 0; i < len(nets); i++ {
		_, aNet, err := net.ParseCIDR(nets[i].cidr)
		if err != nil {
			continue
		}
		for j := i + 1; j < len(nets); j++ {
			_, bNet, err := net.ParseCIDR(nets[j].cidr)
			if err != nil {
				continue
			}
			if aNet.Contains(bNet.IP) || bNet.Contains(aNet.IP) {
				fmt.Fprintf(os.Stderr, "[warn] %s (%s) and %s (%s) appear to overlap\n",
					nets[i].label, nets[i].cidr, nets[j].label, nets[j].cidr)
			}
		}
	}
}

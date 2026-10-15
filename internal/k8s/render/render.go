// Package render provides Go text/template rendering for the BNK manifest
// templates embedded in internal/k8s/manifests. Templates use {{ .Field }}
// syntax (converted from the bash seed's ${VAR} envsubst placeholders — see
// docs/SPEC-go-tracer.md §5 for the per-file mapping table).
//
// Task A defines only the base Render function. Task C/D append one Vars
// struct + one Render<Thing> wrapper per template (nadsVars/RenderNADs,
// cneInstanceVars/RenderCNEInstance, vlansRoutesVars/RenderVlansRoutes,
// licenseCRVars/RenderLicenseCR, egressVars/RenderEgress,
// agentRouteSetterVars/RenderAgentRouteSetter, discoverPodVars/
// RenderDiscoverPod) — purely additive to this file, per the append-only
// seam called out in docs/SPEC-go-tracer.md §8 merge notes.
package render

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// Render executes a Go text/template given in tmpl with data as the
// dot-value and returns the rendered bytes. Returns a descriptive error on
// any parse or execution failure.
func Render(tmpl []byte, data interface{}) ([]byte, error) {
	t, err := template.New("manifest").Parse(string(tmpl))
	if err != nil {
		return nil, fmt.Errorf("render: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render: execute template: %w", err)
	}
	return buf.Bytes(), nil
}

// NADsVars is the Vars struct for templates/nads-hostdevice.yaml.tmpl.
// Field mapping: docs/SPEC-go-tracer.md §5.
type NADsVars struct {
	ExtIface  string
	ExtSelfIP string
	IntIface  string
	IntSelfIP string
}

// RenderNADs renders templates/nads-hostdevice.yaml.tmpl (Phase35).
func RenderNADs(tmpl []byte, v NADsVars) ([]byte, error) {
	return Render(tmpl, v)
}

// CNEInstanceVars is the Vars struct for templates/cneinstance.yaml.tmpl.
// Field mapping: docs/SPEC-go-tracer.md §5.
type CNEInstanceVars struct {
	TmmMtu          string
	ManifestVersion string
	ExtPCI          string
	IntPCI          string
	ImageRegistry   string
}

// RenderCNEInstance renders templates/cneinstance.yaml.tmpl (Phase38). The
// two PCIDEVICE_INTEL_COM_* env entries + TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE
// are the mandatory host-device contract items (reference/TRIAL-NOTES.md
// item 3) — do not drop them.
func RenderCNEInstance(tmpl []byte, v CNEInstanceVars) ([]byte, error) {
	return Render(tmpl, v)
}

// VlansRoutesVars is the Vars struct for templates/vlans-routes.yaml.tmpl.
// Field mapping: docs/SPEC-go-tracer.md §5.
type VlansRoutesVars struct {
	TmmMtu    string
	ExtSelfIP string
	IntSelfIP string
	ExtGwIP   string
	IntGwIP   string
	VxlanPort int

	ExtUnderlayDest      string
	ExtUnderlayPrefixLen int
	MainPodsDest         string
	MainPodsPrefixLen    int
	MainSvcsDest         string
	MainSvcsPrefixLen    int
	TmmPodGW             string
}

// RenderVlansRoutes renders templates/vlans-routes.yaml.tmpl (Phase41).
// route-int-gw is the item that made egress work where the reference
// cluster failed (reference/TRIAL-NOTES.md item 7) — its presence is
// golden-tested, per docs/SPEC-go-tracer.md "Notes for Builder".
func RenderVlansRoutes(tmpl []byte, v VlansRoutesVars) ([]byte, error) {
	return Render(tmpl, v)
}

// LicenseCRVars is the Vars struct for templates/license-cr.yaml.tmpl.
type LicenseCRVars struct {
	// JWT is the raw, whitespace-trimmed license token — never base64, never
	// written to disk (docs/SPEC-go-tracer.md §2 "Secrets constraint").
	JWT string
}

// RenderLicenseCR renders templates/license-cr.yaml.tmpl (Phase40).
func RenderLicenseCR(tmpl []byte, v LicenseCRVars) ([]byte, error) {
	return Render(tmpl, v)
}

// NamespacesVars is the Vars struct for templates/namespaces.yaml.tmpl.
// Field mapping: docs/SPEC-go-tracer.md §5.
type NamespacesVars struct {
	TestNamespace string
}

// RenderNamespaces renders templates/namespaces.yaml.tmpl (Phase30):
// the cert-manager, f5-cne-core, f5-cne-system, f5-bnk-instance, f5-utils, and
// egress test namespaces. TestNamespace is templated to support custom egress namespaces.
func RenderNamespaces(tmpl []byte, v NamespacesVars) ([]byte, error) {
	return Render(tmpl, v)
}

// EgressVars is the Vars struct for templates/egress.yaml.tmpl.
// Field mapping: docs/SPEC-go-tracer.md §5.
type EgressVars struct {
	TestNamespace string
	VxlanKey      int
	VxlanMtu      int
	AgentExtIface string
	VxlanPort     int
	AgentPoolName string
}

// RenderEgress renders templates/egress.yaml.tmpl (Phase50): the
// F5SPKEgress pseudo-CNI vxlan CR + the egress-client test pod.
func RenderEgress(tmpl []byte, v EgressVars) ([]byte, error) {
	return Render(tmpl, v)
}

// AgentRouteSetterVars is the Vars struct for
// templates/agent-route-setter.yaml.tmpl. Field mapping:
// docs/SPEC-go-tracer.md §5.
type AgentRouteSetterVars struct {
	AgentPoolName string
	ExtPodsCidr   string
	ExtGwIP       string
	AgentExtIface string
}

// RenderAgentRouteSetter renders templates/agent-route-setter.yaml.tmpl
// (Phase50): the bnk-agent-route-setter DaemonSet that keeps the
// pseudo-CNI-vxlan egress route alive on agent nodes
// (reference/TRIAL-NOTES.md item 7).
func RenderAgentRouteSetter(tmpl []byte, v AgentRouteSetterVars) ([]byte, error) {
	return Render(tmpl, v)
}

// IngressVxlanRouteVars is one rendered F5SPKStaticRoute entry: an
// agent-pool node's pod CIDR (Destination/PrefixLen) reachable over that
// node's discovered vxlan100 VTEP (Gateway).
type IngressVxlanRouteVars struct {
	Name        string
	Destination string
	PrefixLen   int
	Gateway     string
}

// IngressVxlanRoutesVars is the Vars struct for
// templates/ingress-vxlan-routes.yaml.tmpl — one entry per agent-pool node
// that has a discovered vxlan100 VTEP (Phase51).
type IngressVxlanRoutesVars struct {
	Routes []IngressVxlanRouteVars
}

// RenderIngressVxlanRoutes renders templates/ingress-vxlan-routes.yaml.tmpl
// (Phase51): one F5SPKStaticRoute per agent-pool node with a live vxlan100
// VTEP, fixing the egress/ingress asymmetric-routing gap
// (internal/gcp/phases/phase51_ingress_vxlan.go's doc comment). An empty
// v.Routes renders to an empty byte slice — Phase51 skips applying in that
// case rather than SSA-ing zero objects.
func RenderIngressVxlanRoutes(tmpl []byte, v IngressVxlanRoutesVars) ([]byte, error) {
	return Render(tmpl, v)
}

const (
	InfraName         = "infra"
	InfraExtNetwork   = "external"
	InfraIntNetwork   = "internal"
	InfraListenerPool = "listener-pool"
	InfraEgressSubnet = "192.168.0.0/16"
	InfraEgressPort   = 4789
	InfraRouteVNet    = "vnet"
	InfraRouteDefault = "default"
	TMMK8sRoutesEnv   = "TMM_K8S_ROUTES"
)

// InfraTunnelNetwork is the Infra network the egress tunnel terminates on:
// int-vlan when the pattern has an internal interface, ext-vlan otherwise.
func InfraTunnelNetwork(hasInternal bool) string {
	if hasInternal {
		return InfraIntNetwork
	}
	return InfraExtNetwork
}

// CNEControllerServiceAccount is the ServiceAccount the F5 cne-controller runs as.
const CNEControllerServiceAccount = "f5-cne-controller"

// CNEControllerRBACName is the ClusterRole/ClusterRoleBinding gkebnkctl adds
// so the controller can read EndpointSlices (see templates/cne-controller-rbac.yaml.tmpl).
func CNEControllerRBACName(cl *intent.Cluster) string {
	return cl.Metadata.Name + "-cne-controller-endpointslices"
}

// CNEControllerRBACVars holds the substitution variables for
// templates/cne-controller-rbac.yaml.tmpl.
type CNEControllerRBACVars struct {
	Name           string // <cluster>-cne-controller-endpointslices
	LabName        string // cl.Metadata.Name
	InstanceNS     string // f5-cne-system
	ServiceAccount string // f5-cne-controller
}

// RenderCNEControllerRBAC renders the EndpointSlice + NAD ClusterRole + binding for
// the cne-controller ServiceAccount.
func RenderCNEControllerRBAC(tmpl []byte, cl *intent.Cluster) ([]byte, error) {
	return RenderCNEControllerRBACNamespace(tmpl, cl, "f5-cne-system")
}

// RenderCNEControllerRBACNamespace renders the RBAC with an explicit namespace.
func RenderCNEControllerRBACNamespace(tmpl []byte, cl *intent.Cluster, ns string) ([]byte, error) {
	if cl == nil || cl.Metadata.Name == "" {
		return nil, fmt.Errorf("render cne-controller rbac: cluster name is required")
	}
	if ns == "" {
		ns = "f5-cne-system"
	}
	return renderTemplateString(string(tmpl), CNEControllerRBACVars{
		Name:           CNEControllerRBACName(cl),
		LabName:        cl.Metadata.Name,
		InstanceNS:     ns,
		ServiceAccount: CNEControllerServiceAccount,
	})
}

func renderTemplateString(tmplStr string, data interface{}) ([]byte, error) {
	t, err := template.New("tmpl").Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parsing template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template: %w", err)
	}
	return buf.Bytes(), nil
}

// FLOValuesVars holds the substitution variables for flo-values.yaml.tmpl.
type FLOValuesVars struct {
	CAIssuer      string // <cluster>-ca-cluster-issuer
	FARSecretName string // far-secret
	JWT           string // raw JWT token contents
	ClusterName   string // cl.Metadata.Name
}

// FLOValuesVarsFromCluster derives the FLO values template variables from the cluster intent.
func FLOValuesVarsFromCluster(cl *intent.Cluster, jwt string) FLOValuesVars {
	return FLOValuesVars{
		CAIssuer:      cl.Metadata.Name + "-ca-cluster-issuer",
		FARSecretName: "far-secret",
		JWT:           jwt,
		ClusterName:   cl.Metadata.Name,
	}
}

// RenderFLOValues renders the FLO values template with vars derived from the cluster intent.
func RenderFLOValues(tmpl []byte, cl *intent.Cluster, jwt string) ([]byte, error) {
	vars := FLOValuesVarsFromCluster(cl, jwt)
	return renderTemplateString(string(tmpl), vars)
}

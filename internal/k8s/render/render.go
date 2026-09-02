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
// the f5-bnk, f5-utils, and egress test namespaces. f5-bnk and f5-utils
// are static; TestNamespace is templated to support custom egress namespaces.
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

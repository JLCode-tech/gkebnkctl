package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenNADsVars/goldenCNEInstanceVars/goldenVlansRoutesVars/
// goldenLicenseCRVars are the fixed Vars values used by every golden test
// below — kept as package-level values so the *_test.go files that assert
// load-bearing contract fields (TestRenderNADs_Golden etc.) and the ones
// that assert individual fields can share one canonical fixture.
var (
	goldenNADsVars = NADsVars{
		ExtIface:  "ens5",
		ExtSelfIP: "10.31.0.50",
		IntIface:  "ens6",
		IntSelfIP: "10.51.0.50",
	}
	goldenCNEInstanceVars = CNEInstanceVars{
		TmmMtu:          "1460",
		ManifestVersion: "2.4.0-3.2882.0",
		ExtPCI:          "0000:00:05.0",
		IntPCI:          "0000:00:06.0",
	}
	goldenVlansRoutesVars = VlansRoutesVars{
		TmmMtu:               "1460",
		ExtSelfIP:            "10.31.0.50",
		IntSelfIP:            "10.51.0.50",
		ExtGwIP:              "10.30.0.1",
		IntGwIP:              "10.50.0.1",
		VxlanPort:            5789,
		ExtUnderlayDest:      "10.30.0.0",
		ExtUnderlayPrefixLen: 16,
		MainPodsDest:         "10.66.0.0",
		MainPodsPrefixLen:    16,
		MainSvcsDest:         "10.67.0.0",
		MainSvcsPrefixLen:    20,
		TmmPodGW:             "10.66.1.1",
	}
	goldenLicenseCRVars = LicenseCRVars{
		JWT: "eyFAKE.JWT.TOKEN",
	}
	goldenEgressVars = EgressVars{
		TestNamespace: "egress-test",
		VxlanKey:      100,
		VxlanMtu:      1410,
		AgentExtIface: "ens5",
		VxlanPort:     5789,
		AgentPoolName: "agent-pool",
	}
	goldenAgentRouteSetterVars = AgentRouteSetterVars{
		AgentPoolName: "agent-pool",
		ExtPodsCidr:   "10.31.0.0/24",
		ExtGwIP:       "10.30.0.1",
		AgentExtIface: "ens5",
	}
)

// readTemplate loads a template source from ../manifests/templates by name
// (the embedded FS is Task C's own; render tests read the file directly off
// disk rather than importing the manifests package, keeping render_test.go
// dependency-free of the manifests package per its own doc comment).
func readTemplate(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "manifests", "templates", name))
	if err != nil {
		t.Fatalf("reading template %s: %v", name, err)
	}
	return data
}

// assertGolden renders got against the golden file at testdata/name and
// fails with a diff-friendly message on mismatch. Set UPDATE_GOLDEN=1 to
// (re)write the golden file from got.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing golden file %s: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file %s: %v (run with UPDATE_GOLDEN=1 to create it)", path, err)
	}
	if string(want) != string(got) {
		t.Errorf("rendered output does not match golden file %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

func TestRenderNADs_Golden(t *testing.T) {
	tmpl := readTemplate(t, "nads-hostdevice.yaml.tmpl")
	got, err := RenderNADs(tmpl, goldenNADsVars)
	if err != nil {
		t.Fatalf("RenderNADs: %v", err)
	}
	assertGolden(t, "nads-hostdevice.golden.yaml", got)

	s := string(got)
	if !strings.Contains(s, `"device": "ens5"`) || !strings.Contains(s, `"address": "10.31.0.50/28"`) {
		t.Error("external NAD missing discovered iface/self-IP")
	}
	if !strings.Contains(s, `"device": "ens6"`) || !strings.Contains(s, `"address": "10.51.0.50/28"`) {
		t.Error("internal NAD missing discovered iface/self-IP")
	}
}

// TestRenderCNEInstance_Golden asserts the mandatory host-device contract
// fields (reference/TRIAL-NOTES.md item 3): TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE
// and both PCIDEVICE_INTEL_COM_* entries, per docs/SPEC-go-tracer.md §7.
func TestRenderCNEInstance_Golden(t *testing.T) {
	tmpl := readTemplate(t, "cneinstance.yaml.tmpl")
	got, err := RenderCNEInstance(tmpl, goldenCNEInstanceVars)
	if err != nil {
		t.Fatalf("RenderCNEInstance: %v", err)
	}
	assertGolden(t, "cneinstance.golden.yaml", got)

	s := string(got)
	for _, want := range []string{
		`TMM_MAPRES_ADDL_VETHS_ON_DP`,
		`value: "TRUE"`,
		`name: PCIDEVICE_INTEL_COM_EXT`,
		`value: "0000:00:05.0"`,
		`name: PCIDEVICE_INTEL_COM_INT`,
		`value: "0000:00:06.0"`,
		`manifestVersion: "2.4.0-3.2882.0"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered CNEInstance missing contract field %q", want)
		}
	}
}

// TestRenderVlansRoutes_Golden asserts route-int-gw is present — the item
// that made egress work where the reference cluster failed
// (docs/SPEC-go-tracer.md "Notes for Builder") — plus both self-IPs.
func TestRenderVlansRoutes_Golden(t *testing.T) {
	tmpl := readTemplate(t, "vlans-routes.yaml.tmpl")
	got, err := RenderVlansRoutes(tmpl, goldenVlansRoutesVars)
	if err != nil {
		t.Fatalf("RenderVlansRoutes: %v", err)
	}
	assertGolden(t, "vlans-routes.golden.yaml", got)

	s := string(got)
	if !strings.Contains(s, "name: route-int-gw") {
		t.Fatal("rendered vlans-routes is missing route-int-gw — required for default-egress gw resolution")
	}
	if !strings.Contains(s, "- 10.31.0.50") {
		t.Error("external F5SPKVlan missing ExtSelfIP")
	}
	if !strings.Contains(s, "- 10.51.0.50") {
		t.Error("internal F5SPKVlan missing IntSelfIP")
	}
}

func TestRenderLicenseCR_Golden(t *testing.T) {
	tmpl := readTemplate(t, "license-cr.yaml.tmpl")
	got, err := RenderLicenseCR(tmpl, goldenLicenseCRVars)
	if err != nil {
		t.Fatalf("RenderLicenseCR: %v", err)
	}
	assertGolden(t, "license-cr.golden.yaml", got)

	if !strings.Contains(string(got), `jwt: "eyFAKE.JWT.TOKEN"`) {
		t.Error("rendered License CR missing spec.jwt or JWT is not quoted")
	}
}

// TestRenderEgress_Golden asserts the F5SPKEgress pseudo-CNI vxlan config
// (nodeInterfaceName from Phase25-discovered AGENT_EXT_IFACE, plus
// testNamespace/vxlanKey/vxlanMtu/vxlanPort from cluster.yaml) and the
// egress-client test pod's agentPool nodeSelector both render correctly.
func TestRenderEgress_Golden(t *testing.T) {
	tmpl := readTemplate(t, "egress.yaml.tmpl")
	got, err := RenderEgress(tmpl, goldenEgressVars)
	if err != nil {
		t.Fatalf("RenderEgress: %v", err)
	}
	assertGolden(t, "egress.golden.yaml", got)

	s := string(got)
	for _, want := range []string{
		"kind: F5SPKEgress",
		"- egress-test",
		"key: 100",
		"mtu: 1410",
		"nodeInterfaceName: ens5",
		"port: 5789",
		"name: egress-client",
		"namespace: egress-test",
		"cloud.google.com/gke-nodepool: agent-pool",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered egress manifest missing %q", want)
		}
	}
}

// TestRenderAgentRouteSetter_Golden asserts the agent-node route-reconcile
// DaemonSet carries the external-pods-CIDR/gateway/iface triple
// (reference/TRIAL-NOTES.md item 7) and targets the configured agent pool.
func TestRenderAgentRouteSetter_Golden(t *testing.T) {
	tmpl := readTemplate(t, "agent-route-setter.yaml.tmpl")
	got, err := RenderAgentRouteSetter(tmpl, goldenAgentRouteSetterVars)
	if err != nil {
		t.Fatalf("RenderAgentRouteSetter: %v", err)
	}
	assertGolden(t, "agent-route-setter.golden.yaml", got)

	s := string(got)
	for _, want := range []string{
		"kind: DaemonSet",
		"name: bnk-agent-route-setter",
		"cloud.google.com/gke-nodepool: agent-pool",
		"want_cidr='10.31.0.0/24'",
		"want_gw='10.30.0.1'",
		"want_dev='ens5'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered agent-route-setter DS missing %q", want)
		}
	}
}

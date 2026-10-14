package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/yaml"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/migrate"
	"github.com/JLCode-tech/gkebnkctl/internal/manifest"
)

// A small 2.3 cluster: CNEInstance, one VLAN, the BNK GatewayClass, one
// Gateway with its F5BnkGateway pool.
const cliFixture23 = `
apiVersion: k8s.f5.com/v1
kind: CNEInstance
metadata: {name: lab-bnk, namespace: f5-cne-system}
spec:
  manifestVersion: "2.3.3-3.2598.3-0.0.509"
  networkAttachments: [external-netdevice]
  advanced: {cneController: {env: [{name: TMM_DEFAULT_MTU, value: "9000"}]}}
---
apiVersion: k8s.f5net.com/v1
kind: F5SPKVlan
metadata: {name: ext-vlan, namespace: f5-cne-system}
spec: {name: ext-vlan, interfaces: ["1.1"], selfip_v4s: ["10.0.10.240"], tag: 0}
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata: {name: lab-gatewayclass}
spec: {controllerName: f5.com/f5-cne-system-f5-cne-controller}
---
apiVersion: k8s.f5net.com/v1
kind: F5BnkGateway
metadata: {name: web, namespace: apps}
spec:
  ingressConfig:
    defaultListenerNetworks:
    - {name: ext, ipv4BaseCidr: "10.0.10.0/24", startAddress: 10.0.10.100, endAddress: 10.0.10.110}
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: web, namespace: apps}
spec:
  gatewayClassName: lab-gatewayclass
  addresses: [{type: IPAddress, value: 10.0.10.101}]
  listeners: [{name: http, protocol: HTTP, port: 80}]
`

var cliGVRs = map[string]schema.GroupVersionResource{
	"CNEInstance":      migrate.CNEInstanceGVR,
	"F5SPKVlan":        migrate.VlanGVR,
	"F5SPKStaticRoute": migrate.StaticRouteGVR,
	"Vrf":              migrate.VrfGVR,
	"Vxlan":            migrate.VxlanGVR,
	"F5SPKEgress":      migrate.EgressGVR,
	"F5SPKSnatpool":    migrate.SnatpoolGVR,
	"F5BnkGateway":     migrate.BnkGatewayGVR,
	"BNKSecPolicy":     migrate.BNKSecPolGVR,
	"BNKNetPolicy":     migrate.BNKNetPolGVR,
	"Gateway":          migrate.GatewayGVR,
	"GatewayClass":     migrate.GatewayClassGVR,
	"Infra":            migrate.InfraGVR,
}

func fakeMigrateDynamic(t *testing.T) *dynamicfake.FakeDynamicClient {
	t.Helper()
	s := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{}
	for kind, gvr := range cliGVRs {
		s.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind), &unstructured.Unstructured{})
		s.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind+"List"), &unstructured.UnstructuredList{})
		listKinds[gvr] = kind + "List"
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(s, listKinds)
	for _, doc := range strings.Split(cliFixture23, "\n---\n") {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatal(err)
		}
		obj := &unstructured.Unstructured{Object: m}
		ri := dyn.Resource(cliGVRs[obj.GetKind()])
		var err error
		if obj.GetNamespace() != "" {
			_, err = ri.Namespace(obj.GetNamespace()).Create(context.Background(), obj, metav1.CreateOptions{})
		} else {
			_, err = ri.Create(context.Background(), obj, metav1.CreateOptions{})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return dyn
}

func resetMigrateFlags() {
	flagBnkMigrateDryRun = false
	flagBnkMigrateApply = false
	flagBnkMigrateKubeconfig = ""
	flagBnkMigrateConfig = ""
	flagBnkMigrateNamespace = migrate.DefaultInstanceNamespace
	flagBnkMigrateInfraName = migrate.DefaultInfraName
	flagBnkMigrateGatewayClass = ""
	flagBnkMigrateSingleSelfIP = false
	flagBnkMigrateWait = 0
	flagOutput = "text"
}

func TestBnkMigrateAndUpgradeRegistered(t *testing.T) {
	var found []string
	for _, sub := range bnkCmd.Commands() {
		found = append(found, sub.Name())
	}
	for _, want := range []string{"migrate-2.4", "upgrade"} {
		ok := false
		for _, f := range found {
			if f == want {
				ok = true
			}
		}
		if !ok {
			t.Errorf("bnk %s not registered (have %v)", want, found)
		}
	}
	for _, flag := range []string{"dry-run", "apply", "kubeconfig", "config", "namespace", "infra-name", "gateway-class", "single-self-ip", "wait"} {
		if bnkMigrateCmd.Flags().Lookup(flag) == nil {
			t.Errorf("bnk migrate-2.4 lacks --%s", flag)
		}
	}
	if f := bnkMigrateCmd.Flags().ShorthandLookup("f"); f == nil || f.Name != "config" {
		t.Error("bnk migrate-2.4 -f is not --config")
	}
	for _, flag := range []string{"config", "kubeconfig", "manifest-version", "flo-version", "namespace", "instance", "dry-run", "timeout"} {
		if bnkUpgradeCmd.Flags().Lookup(flag) == nil {
			t.Errorf("bnk upgrade lacks --%s", flag)
		}
	}
	if def := bnkUpgradeCmd.Flags().Lookup("manifest-version").DefValue; def != manifest.DefaultManifestVersion {
		t.Errorf("bnk upgrade --manifest-version default = %q", def)
	}
}

func TestBnkMigrate_DryRunPrintsPlan(t *testing.T) {
	resetMigrateFlags()
	defer resetMigrateFlags()
	orig := bnkMigrateDynamicClient
	defer func() { bnkMigrateDynamicClient = orig }()
	dyn := fakeMigrateDynamic(t)
	bnkMigrateDynamicClient = func(string) (dynamic.Interface, error) { return dyn, nil }

	flagBnkMigrateDryRun = true
	var out bytes.Buffer
	bnkMigrateCmd.SetOut(&out)
	defer bnkMigrateCmd.SetOut(nil)
	if err := runBnkMigrate(bnkMigrateCmd, nil); err != nil {
		t.Fatalf("runBnkMigrate: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"# Infra f5-cne-system/infra",
		"kind: Infra",
		"# GatewaySettings apps/web",
		"name: apps-web",
		"rangeStart: 10.0.10.100",
		"# Gateway apps/web",
		"kind: GatewaySettings",
		"parametersRef:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "kind: EgressGateway") {
		t.Error("EgressGateway generated without an F5SPKEgress")
	}
	// Documents are separated, and nothing was written to the cluster.
	if strings.Count(got, "\n---\n") != 2 {
		t.Errorf("want 3 documents, got:\n%s", got)
	}
	if _, err := dyn.Resource(migrate.InfraGVR).Namespace("f5-cne-system").Get(context.Background(), "infra", metav1.GetOptions{}); err == nil {
		t.Error("dry-run created the Infra CR")
	}
}

func TestBnkMigrate_DryRunJSON(t *testing.T) {
	resetMigrateFlags()
	defer resetMigrateFlags()
	orig := bnkMigrateDynamicClient
	defer func() { bnkMigrateDynamicClient = orig }()
	dyn := fakeMigrateDynamic(t)
	bnkMigrateDynamicClient = func(string) (dynamic.Interface, error) { return dyn, nil }
	flagOutput = "json"
	var out bytes.Buffer
	bnkMigrateCmd.SetOut(&out)
	defer bnkMigrateCmd.SetOut(nil)
	if err := runBnkMigrate(bnkMigrateCmd, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema": "gkebnkctl.migrate-2.4/v1"`, `"kind": "Infra"`, `"F5SPKVlan: 1"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("json missing %s:\n%s", want, out.String())
		}
	}
}

// recordingApplier stands in for the server-side applier.
type recordingApplier struct{ kinds []string }

func (r *recordingApplier) Apply(_ context.Context, obj *unstructured.Unstructured) error {
	r.kinds = append(r.kinds, obj.GetKind())
	return nil
}

func TestBnkMigrate_Apply(t *testing.T) {
	resetMigrateFlags()
	defer resetMigrateFlags()
	origDyn, origApplier := bnkMigrateDynamicClient, bnkMigrateApplier
	defer func() { bnkMigrateDynamicClient, bnkMigrateApplier = origDyn, origApplier }()
	dyn := fakeMigrateDynamic(t)
	bnkMigrateDynamicClient = func(string) (dynamic.Interface, error) { return dyn, nil }
	rec := &recordingApplier{}
	bnkMigrateApplier = func(string) (migrate.Applier, error) { return rec, nil }

	flagBnkMigrateApply = true
	flagBnkMigrateWait = 0
	var out bytes.Buffer
	bnkMigrateCmd.SetOut(&out)
	defer bnkMigrateCmd.SetOut(nil)
	if err := runBnkMigrate(bnkMigrateCmd, nil); err != nil {
		t.Fatalf("runBnkMigrate --apply: %v", err)
	}
	if strings.Join(rec.kinds, ",") != "Infra,GatewaySettings,Gateway" {
		t.Errorf("applied %v", rec.kinds)
	}
	if out.Len() != 0 {
		t.Errorf("--apply printed manifests on stdout:\n%s", out.String())
	}
}

func TestBnkMigrate_FlagErrors(t *testing.T) {
	resetMigrateFlags()
	defer resetMigrateFlags()
	flagBnkMigrateDryRun, flagBnkMigrateApply = true, true
	if err := runBnkMigrate(bnkMigrateCmd, nil); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v", err)
	}
	resetMigrateFlags()
	orig := bnkMigrateDynamicClient
	defer func() { bnkMigrateDynamicClient = orig }()
	bnkMigrateDynamicClient = func(string) (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	if err := runBnkMigrate(bnkMigrateCmd, nil); err == nil || !strings.Contains(err.Error(), "no kubeconfig") {
		t.Errorf("err = %v", err)
	}
}

// fakeUpgradeHelm is the minimal HelmInstaller the CLI dry-run needs.
type fakeUpgradeHelm struct{ upgraded bool }

func (f *fakeUpgradeHelm) List(string, string) ([]*release.Release, error) {
	return []*release.Release{{Name: "f5-lifecycle-operator", Chart: &chart.Chart{Metadata: &chart.Metadata{Version: "v2.21.13-0.0.64"}}, Info: &release.Info{Status: release.StatusDeployed}}}, nil
}
func (f *fakeUpgradeHelm) Install(string, string, *chart.Chart, map[string]interface{}) (*release.Release, error) {
	return nil, errors.New("unexpected install")
}
func (f *fakeUpgradeHelm) Upgrade(string, string, *chart.Chart, map[string]interface{}) (*release.Release, error) {
	f.upgraded = true
	return &release.Release{}, nil
}
func (f *fakeUpgradeHelm) Uninstall(string, string) error { return nil }
func (f *fakeUpgradeHelm) PullAndLoad(_, v string) (*chart.Chart, error) {
	return &chart.Chart{Metadata: &chart.Metadata{Version: v}}, nil
}

// writeUpgradeCluster writes a cluster.yaml with FAR + JWT files next to it.
func writeUpgradeCluster(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"far.json": "eyJmYWtlIjoxfQ==", "license.jwt": "eyJhbGciOiJSUzUxMiJ9.e30.sig"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cluster := `apiVersion: gkebnkctl/v1
kind: Cluster
metadata:
  name: bnk-upgrade-test
  project: test-project
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
cluster:
  kubernetesVersion: "1.35"
  defaultPool:
    machineType: e2-standard-4
    nodeCount: 3
  tmmPool:
    machineType: e2-standard-8
    nodeCount: 1
    maxPodsPerNode: 8
  agentPool:
    machineType: e2-standard-4
    nodeCount: 1
    maxPodsPerNode: 8
bnk:
  manifestVersion: "2.3.3"
  jwt: ./license.jwt
  farArchive: ./far.json
`

	path := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(path, []byte(cluster), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBnkUpgrade_RequiresConfig(t *testing.T) {
	flagBnkUpgradeConfig = ""
	if err := runBnkUpgrade(bnkUpgradeCmd, nil); err == nil || !strings.Contains(err.Error(), "--config") {
		t.Errorf("err = %v", err)
	}
}

func TestBnkUpgrade_DryRun(t *testing.T) {
	origDeps := bnkUpgradeDeps
	defer func() {
		bnkUpgradeDeps = origDeps
		flagBnkUpgradeConfig = ""
		flagBnkUpgradeDryRun = false
		flagOutput = "text"
	}()
	helm := &fakeUpgradeHelm{}
	dyn := fakeMigrateDynamic(t)
	var gotFAR string
	bnkUpgradeDeps = func(_ string, far string) (migrate.UpgradeDeps, error) {
		gotFAR = far
		return migrate.UpgradeDeps{Helm: helm, Dyn: dyn}, nil
	}
	flagBnkUpgradeConfig = writeUpgradeCluster(t)
	flagBnkUpgradeDryRun = true
	flagBnkUpgradeNamespace = migrate.DefaultInstanceNamespace
	flagBnkUpgradeManifest = "2.4.0"
	flagOutput = "json"
	var out bytes.Buffer
	bnkUpgradeCmd.SetOut(&out)
	defer bnkUpgradeCmd.SetOut(nil)
	if err := runBnkUpgrade(bnkUpgradeCmd, nil); err != nil {
		t.Fatalf("runBnkUpgrade: %v", err)
	}
	if gotFAR != "eyJmYWtlIjoxfQ==" {
		t.Errorf("FAR key passed to the helm client = %q", gotFAR)
	}
	if helm.upgraded {
		t.Error("dry-run ran helm upgrade")
	}
	for _, want := range []string{`"floTo":"v2.30.0-0.5.2"`, `"manifestTo":"2.4.0"`, `"cneInstance":"lab-bnk"`, `"dryRun":true`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("json missing %s:\n%s", want, out.String())
		}
	}
}

func TestFloUpgradeInputs(t *testing.T) {
	path := writeUpgradeCluster(t)
	cl, err := intent.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	far, values, err := floUpgradeInputs(cl)
	if err != nil {
		t.Fatal(err)
	}
	if far != "eyJmYWtlIjoxfQ==" {
		t.Errorf("far = %q", far)
	}
	lic, _ := values["license"].(map[string]any)
	if lic["jwt"] != "eyJhbGciOiJSUzUxMiJ9.e30.sig" || lic["friendlyName"] != "BNK bnk-upgrade-test" {
		t.Errorf("license values = %v", lic)
	}
	if values["containerPlatform"] != "GCP" {
		t.Errorf("containerPlatform = %v", values["containerPlatform"])
	}
}

func TestResolveClusterPath(t *testing.T) {
	if got := resolveClusterPath("/a/b/cluster.yaml", "./far.json"); got != "/a/b/far.json" {
		t.Errorf("relative = %q", got)
	}
	if got := resolveClusterPath("/a/b/cluster.yaml", "/x/far.json"); got != "/x/far.json" {
		t.Errorf("absolute = %q", got)
	}
	if got := resolveClusterPath("", "far.json"); got != "far.json" {
		t.Errorf("no source = %q", got)
	}
}

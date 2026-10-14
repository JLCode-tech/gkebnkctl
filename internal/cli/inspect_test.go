package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/bnkscan"
)

const scanFixture = `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: infras.gateway.k8s.f5.com}
spec: {group: gateway.k8s.f5.com}
---
apiVersion: gateway.k8s.f5.com/v1alpha1
kind: Infra
metadata: {name: infra, namespace: f5-cne-system}
status: {conditions: [{type: Programmed, status: "True"}]}
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata: {name: demo-gatewayclass}
spec: {controllerName: f5.com/f5-cne-system-f5-cne-controller}
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: gw, namespace: default}
spec:
  gatewayClassName: demo-gatewayclass
  addresses: [{type: IPAddress, value: 10.0.10.150}]
  listeners: [{name: http, protocol: HTTP, port: 80}]
status: {conditions: [{type: Accepted, status: "True"}, {type: Programmed, status: "True"}]}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: tools, namespace: default}
spec:
  parentRefs: [{name: gw}]
  hostnames: [tools.demo.internal]
  rules:
  - matches: [{path: {type: PathPrefix, value: /mcp}}]
    backendRefs: [{name: tool-server, port: 8000}]
`

var scanGVRs = map[string]schema.GroupVersionResource{
	"CustomResourceDefinition": bnkscan.CRDGVR,
	"Infra":                    bnkscan.InfraGVR,
	"GatewaySettings":          bnkscan.GatewaySettingsGVR,
	"EgressGateway":            bnkscan.EgressGatewayGVR,
	"SecPolicy":                bnkscan.SecPolicyGVR,
	"NetPolicy":                bnkscan.NetPolicyGVR,
	"F5BigPersistenceProfile":  bnkscan.PersistenceProfileGVR,
	"GatewayClass":             bnkscan.GatewayClassGVR,
	"Gateway":                  bnkscan.GatewayGVR,
	"HTTPRoute":                bnkscan.HTTPRouteGVR,
	"F5BnkGateway":             bnkscan.BnkGatewayGVR,
	"BNKSecPolicy":             bnkscan.BNKSecPolGVR,
	"BNKNetPolicy":             bnkscan.BNKNetPolGVR,
	"F5SPKVlan":                bnkscan.VlanGVR,
	"F5SPKEgress":              bnkscan.EgressGVR,
}

func fakeScanClients(t *testing.T, fixture string, available int32) (dynamic.Interface, kubernetes.Interface) {
	t.Helper()
	s := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{}
	for kind, gvr := range scanGVRs {
		s.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind), &unstructured.Unstructured{})
		s.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind+"List"), &unstructured.UnstructuredList{})
		listKinds[gvr] = kind + "List"
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(s, listKinds)
	for _, doc := range strings.Split(fixture, "\n---\n") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatal(err)
		}
		obj := &unstructured.Unstructured{Object: m}
		ri := dyn.Resource(scanGVRs[obj.GetKind()])
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
	one := int32(1)
	cs := k8sfake.NewClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: bnkscan.DefaultControllerName, Namespace: bnkscan.DefaultControllerNamespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: available},
	})
	return dyn, cs
}

func TestResolveKubeconfigForStatus(t *testing.T) {
	t.Parallel()

	t.Run("nil state falls back to host default", func(t *testing.T) {
		got := resolveKubeconfigForStatus(nil)
		_ = got
	})

	t.Run("state without KUBECONFIG_PATH falls back to host default", func(t *testing.T) {
		st, _ := stateFrom(t, map[string]string{"VPC_ID": "vpc-123"})
		got := resolveKubeconfigForStatus(st)
		_ = got
	})

	t.Run("state with existing KUBECONFIG_PATH returns that path", func(t *testing.T) {
		dir := t.TempDir()
		kcFile := filepath.Join(dir, "kubeconfig")
		if err := os.WriteFile(kcFile, []byte("fake-kubeconfig"), 0o600); err != nil {
			t.Fatal(err)
		}

		st, _ := stateFrom(t, map[string]string{"KUBECONFIG_PATH": kcFile})
		got := resolveKubeconfigForStatus(st)
		if got != kcFile {
			t.Errorf("resolveKubeconfigForStatus = %q, want %q", got, kcFile)
		}
	})

	t.Run("state with relative KUBECONFIG_PATH resolves via state dir", func(t *testing.T) {
		dir := t.TempDir()
		kcFile := filepath.Join(dir, "kubeconfig")
		if err := os.WriteFile(kcFile, []byte("fake-kubeconfig"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.env"), []byte("KUBECONFIG_PATH=.gkebnkctl/mycluster/kubeconfig\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := state.Load(dir)
		if err != nil {
			t.Fatalf("state.Load: %v", err)
		}
		got := resolveKubeconfigForStatus(st)
		if got != kcFile {
			t.Errorf("resolveKubeconfigForStatus = %q, want %q (state dir fallback)", got, kcFile)
		}
	})
}

func TestResolveKubeconfigFromConfig(t *testing.T) {
	t.Parallel()

	t.Run("empty configPath returns empty string", func(t *testing.T) {
		got, err := resolveKubeconfigFromConfig("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("nonexistent config file returns error", func(t *testing.T) {
		_, err := resolveKubeconfigFromConfig("/nonexistent/path/cluster.yaml")
		if err == nil {
			t.Fatal("expected error for nonexistent config, got nil")
		}
	})
}

func stateFrom(t *testing.T, kv map[string]string) (*state.State, string) {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	for k, v := range kv {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "state.env"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return st, dir
}

func TestTMMNodeLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kv   map[string]string
		want string
	}{
		{"node name preferred", map[string]string{"TMM_NODE_NAME": "gke-tmm-node-1", "TMM_INSTANCE_ID": "i-abc"}, "gke-tmm-node-1"},
		{"tmm node fallback", map[string]string{"TMM_NODE": "gke-tmm-node-2"}, "gke-tmm-node-2"},
		{"instance id fallback", map[string]string{"TMM_INSTANCE_ID": "i-abc"}, "i-abc"},
		{"neither", map[string]string{}, "not provisioned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := stateFrom(t, tc.kv)
			if got := tmmNodeLine(st); got != tc.want {
				t.Errorf("tmmNodeLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBNKActivationLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		kv         map[string]string
		wantPrefix string
	}{
		{"not activated", map[string]string{}, "not activated"},
		{"activating (license applied, not ready)", map[string]string{"LICENSE_APPLIED_AT": "2026-05-22T10:00:00Z", "LICENSE_NAME": "lic-1"}, "activating"},
		{"active (ready + license)", map[string]string{"CNEINSTANCE_READY_AT": "2026-05-22T11:00:00Z", "LICENSE_NAME": "lic-1"}, "Active"},
		{"active (ready only)", map[string]string{"CNEINSTANCE_READY_AT": "2026-05-22T11:00:00Z"}, "Active"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := stateFrom(t, tc.kv)
			if got := bnkActivationLine(st); !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("bnkActivationLine = %q, want prefix %q", got, tc.wantPrefix)
			}
		})
	}
}

func TestForgeLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kv   map[string]string
		want string
	}{
		{"not linked", map[string]string{}, "not linked"},
		{"status only", map[string]string{"FORGE_STATUS": "registered"}, "registered"},
		{"status + project", map[string]string{"FORGE_STATUS": "registered", "FORGE_PROJECT_ID": "proj-9"}, "registered (project proj-9)"},
		{"project only -> unknown status", map[string]string{"FORGE_PROJECT_ID": "proj-9"}, "unknown (project proj-9)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := stateFrom(t, tc.kv)
			if got := forgeLine(st); got != tc.want {
				t.Errorf("forgeLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLastPhaseAppliedLine(t *testing.T) {
	t.Parallel()

	stEmpty, _ := stateFrom(t, map[string]string{"VPC_ID": "vpc-1"})
	if got := lastPhaseAppliedLine(stEmpty); !strings.Contains(got, "no phase timestamps") {
		t.Errorf("empty: got %q, want a 'no phase timestamps' hint", got)
	}

	stMany, _ := stateFrom(t, map[string]string{
		"NADS_APPLIED_AT":      "2026-05-22T09:00:00Z",
		"LICENSE_APPLIED_AT":   "dry-run",
		"CNEINSTANCE_READY_AT": "2026-05-22T12:30:00Z",
		"FLO_INSTALLED_AT":     "2026-05-22T10:15:00Z",
	})
	got := lastPhaseAppliedLine(stMany)
	if !strings.HasPrefix(got, "CNEINSTANCE_READY_AT at ") {
		t.Errorf("many: got %q, want most-recent key CNEINSTANCE_READY_AT", got)
	}

	stNumeric, _ := stateFrom(t, map[string]string{
		"PHASE_30_APPLIED_AT": "2026-05-22T13:00:00Z",
		"PHASE_40_APPLIED_AT": "2026-05-22T14:00:00Z",
	})
	gotNum := lastPhaseAppliedLine(stNumeric)
	if !strings.HasPrefix(gotNum, "PHASE_40_APPLIED_AT at ") {
		t.Errorf("numeric: got %q, want PHASE_40_APPLIED_AT", gotNum)
	}
}

func TestLoadStatusState(t *testing.T) {
	t.Parallel()

	if _, ok := loadStatusState("", ""); ok {
		t.Error("expected (nil,false) with no config and no fallback name")
	}

	st, dir := stateFrom(t, map[string]string{"VPC_ID": "vpc-xyz"})
	root := t.TempDir()
	clusterName := "fallbackcl"
	stateDir := filepath.Join(root, ".gkebnkctl", clusterName)
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(dir, "state.env"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.env"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	loaded, ok := loadStatusState("", clusterName)
	if !ok {
		t.Fatal("expected fallback-name lookup to find the staged state.env")
	}
	if loaded.Get("VPC_ID") != "vpc-xyz" {
		t.Errorf("loaded wrong state: VPC_ID=%q", loaded.Get("VPC_ID"))
	}

	if _, ok := loadStatusState("", "doesnotexist"); ok {
		t.Error("expected not-found for a cluster with no state.env")
	}

	_ = st
}

func TestWriteStatusDemoBanner(t *testing.T) {
	t.Parallel()

	future := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		name       string
		kv         map[string]string
		wantBanner bool
		wantSubstr string
		wantAbsent string
	}{
		{name: "demo + future expiry", kv: map[string]string{"DEMO_MODE": "true", "DEMO_EXPIRY": future}, wantBanner: true, wantSubstr: "in "},
		{name: "demo + past expiry", kv: map[string]string{"DEMO_MODE": "true", "DEMO_EXPIRY": past}, wantBanner: true, wantSubstr: "EXPIRED"},
		{name: "demo + missing expiry", kv: map[string]string{"DEMO_MODE": "true"}, wantBanner: true, wantAbsent: "expires"},
		{name: "non-demo cluster", kv: map[string]string{"VPC_ID": "vpc-1"}, wantBanner: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var st *state.State
			if tc.kv != nil {
				st, _ = stateFrom(t, tc.kv)
			}
			var buf bytes.Buffer
			writeStatusDemoBanner(&buf, st)
			got := buf.String()

			if tc.wantBanner && !strings.Contains(got, "⚠ DEMO") {
				t.Errorf("expected ⚠ DEMO banner, got: %q", got)
			}
			if !tc.wantBanner && got != "" {
				t.Errorf("expected no output for non-demo cluster, got: %q", got)
			}
			if tc.wantSubstr != "" && !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("expected %q in output, got: %q", tc.wantSubstr, got)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("expected %q to be absent from output, got: %q", tc.wantAbsent, got)
			}
		})
	}
}

func TestWriteStatusDemoBannerNilState(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writeStatusDemoBanner(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for nil state, got: %q", buf.String())
	}
}

func TestWriteStatusBNK(t *testing.T) {
	t.Run("scan error is one line", func(t *testing.T) {
		var out bytes.Buffer
		writeStatusBNK(&out, nil, errors.New("connection refused"))
		if got := out.String(); got != "BNK API:\t(scan failed: connection refused)\n" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("programmed 2.4 cluster", func(t *testing.T) {
		dyn, cs := fakeScanClients(t, scanFixture, 1)
		idx, err := bnkscan.Scan(context.Background(), dyn, cs, bnkscan.Options{})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		writeStatusBNK(&out, idx, nil)
		for _, want := range []string{
			"BNK API:\t2.4 (CRD groups: gateway.k8s.f5.com)",
			"Infra:\t1/1 Programmed",
			"Gateways:\t1/1 Accepted+Programmed",
			"CNE controller:\tf5-cne-system/f5-cne-controller 1/1 available",
			"MCP endpoints:\t1",
			"BNK readiness:\tready",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q in\n%s", want, out.String())
			}
		}
		if strings.Contains(out.String(), "BNK migration") {
			t.Errorf("no migration line on a clean 2.4 cluster:\n%s", out.String())
		}
	})

	t.Run("controller down is not ready", func(t *testing.T) {
		dyn, cs := fakeScanClients(t, scanFixture, 0)
		idx, err := bnkscan.Scan(context.Background(), dyn, cs, bnkscan.Options{})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		writeStatusBNK(&out, idx, nil)
		if !strings.Contains(out.String(), "BNK readiness:\tnot ready -- deployment f5-cne-system/f5-cne-controller: 0/1 replicas available") {
			t.Errorf("output:\n%s", out.String())
		}
	})

	t.Run("no BNK", func(t *testing.T) {
		dyn, cs := fakeScanClients(t, "", 0)
		idx, err := bnkscan.Scan(context.Background(), dyn, cs, bnkscan.Options{})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		writeStatusBNK(&out, idx, nil)
		if got := out.String(); got != "BNK API:\tnone\nBNK readiness:\tnot installed\n" {
			t.Errorf("got %q", got)
		}
	})
}

func TestLogsGovernanceFlagsAndTMMComponent(t *testing.T) {
	for _, flag := range []string{"governance", "mcp"} {
		if logsCmd.Flags().Lookup(flag) == nil {
			t.Errorf("logs --%s missing", flag)
		}
	}
	comp := lookupComponent("tmm")
	if comp == nil {
		t.Fatal("tmm component missing")
	}
	if comp.Ns != k8s.TMMLogNamespace || comp.Selector != k8s.TMMLogPodSelector {
		t.Errorf("tmm component = %+v", comp)
	}
}

func TestFormatGovernance(t *testing.T) {
	rec := forge.GovernanceRecord{Status: "200", RPCMethod: "tools/call", Tool: "forecast", Session: "abc-123", LatencyMS: 42, Action: forge.ActionAllow}
	if got := formatGovernance(rec); got != "[GOV] 200 tools/call tool=forecast session=abc-123 latency=42ms action=allow" {
		t.Errorf("got %q", got)
	}
	if got := formatGovernance(forge.GovernanceRecord{Status: "429", Action: forge.ActionRateLimited}); got != "[GOV] 429 - tool=- session=- latency=0ms action=rate_limited" {
		t.Errorf("empty fields: %q", got)
	}
}

func TestGovernanceFilter(t *testing.T) {
	const rec1 = `BNKGOV {"job":"llm-gateway","model":"mcp","status":"200","latency_ms":12,"client":"c","action":"allow","rpc_method":"tools/call","tool":"forecast","session":"s1"}`
	const rec2 = `BNKGOV {"job":"llm-gateway","model":"mcp","status":"403","latency_ms":3,"client":"c","action":"tool_forbidden"}`

	t.Run("filters, formats and survives split writes", func(t *testing.T) {
		var out bytes.Buffer
		g := newGovernanceFilter(&out, false)
		stream := "plain tmm line\n" + rec1 + "\n" + "<134>Sep 14 tmm[1]: " + rec2 + "\nno newline tail"
		for i := 0; i < len(stream); i += 7 {
			end := i + 7
			if end > len(stream) {
				end = len(stream)
			}
			if _, err := g.Write([]byte(stream[i:end])); err != nil {
				t.Fatal(err)
			}
		}
		g.Flush()
		want := "[GOV] 200 tools/call tool=forecast session=s1 latency=12ms action=allow\n" +
			"[GOV] 403 - tool=- session=- latency=3ms action=tool_forbidden\n"
		if out.String() != want {
			t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
		}
		if g.Records != 2 || g.Dropped != 2 {
			t.Errorf("records=%d dropped=%d", g.Records, g.Dropped)
		}
	})

	t.Run("mcp only keeps records with rpc_method", func(t *testing.T) {
		var out bytes.Buffer
		g := newGovernanceFilter(&out, true)
		if _, err := g.Write([]byte(rec1 + "\n" + rec2 + "\n")); err != nil {
			t.Fatal(err)
		}
		if g.Records != 1 || g.Dropped != 1 || !strings.HasPrefix(out.String(), "[GOV] 200 tools/call") {
			t.Errorf("records=%d dropped=%d out=%q", g.Records, g.Dropped, out.String())
		}
	})
}

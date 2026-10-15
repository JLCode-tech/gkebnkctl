package topology_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/topology"
)

// minimalCluster returns a minimal valid *intent.Cluster for testing without
// needing a real cluster.yaml on disk.
func minimalCluster() *intent.Cluster {
	return &intent.Cluster{
		APIVersion: "gkebnkctl/v1",
		Kind:       "Cluster",
		Metadata: intent.Metadata{
			Name:    "test-cluster",
			Project: "my-gcp-project",
			Zone:    "us-central1-a",
		},
		Region:  "us-central1",
		Pattern: "host-device",
		Network: intent.Network{
			Main: intent.MainNetworkSpec{
				VPC:           "bnkhd-vpc",
				Subnet:        "bnkhd-subnet",
				CIDR:          "10.60.0.0/24",
				PodsRangeName: "bnkhd-pods",
				PodsCIDR:      "10.66.0.0/16",
				SvcsRangeName: "bnkhd-svcs",
				SvcsCIDR:      "10.67.0.0/20",
			},
			External: intent.DataplaneSpec{
				VPC:           "bnkhd-ext",
				Subnet:        "bnkhd-ext",
				CIDR:          "10.30.0.0/24",
				MTU:           8896,
				PodsRangeName: "tmm-ext-pods",
				PodsCIDR:      "10.31.0.0/24",
				UnderlayCIDR:  "10.30.0.0/16",
			},
			Internal: intent.DataplaneSpec{
				VPC:           "bnkhd-int",
				Subnet:        "bnkhd-int",
				CIDR:          "10.50.0.0/24",
				MTU:           8896,
				PodsRangeName: "tmm-int-pods",
				PodsCIDR:      "10.51.0.0/24",
			},
			AllowInternalCidr: "10.0.0.0/8",
		},
		ClusterSpec: intent.ClusterSpec{
			KubernetesVersion: "1.35",
			DefaultPool: intent.NodePoolSpec{
				MachineType: "e2-standard-4",
				NodeCount:   3,
			},
			TmmPool: intent.NodePoolSpec{
				Name:           "tmm-pool",
				MachineType:    "e2-standard-8",
				NodeCount:      1,
				MaxPodsPerNode: 8,
			},
			AgentPool: intent.NodePoolSpec{
				Name:           "agent-pool",
				MachineType:    "e2-standard-4",
				NodeCount:      1,
				MaxPodsPerNode: 8,
			},
		},
		Bnk: intent.BnkSpec{
			ManifestVersion: "2.4.0",
			TmmMtu:          "1460",
			JwtPath:         "/tmp/license.jwt",
		},
		Egress: intent.EgressSpec{
			TestNamespace: "egress-test",
			VxlanPort:     5789,
			VxlanKey:      100,
			VxlanMtu:      1410,
		},
	}
}

// writeStateEnv writes a minimal state.env file to dir and returns a loaded *state.State.
func writeStateEnv(t *testing.T, dir string, kvs map[string]string) *state.State {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var lines []string
	for k, v := range kvs {
		lines = append(lines, k+"="+v)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "state.env"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing state.env: %v", err)
	}
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return st
}

// TestBuild_StateOverlaysIntent verifies that non-empty state keys win over intent values.
func TestBuild_StateOverlaysIntent(t *testing.T) {
	cl := minimalCluster()
	dir := t.TempDir()
	st := writeStateEnv(t, dir, map[string]string{
		"TMM_NODE_NAME":           "gke-tmm-node-1",
		"AGENT_NODE_NAME":         "gke-agent-node-1",
		"TMM_EXT_SELFIP":          "10.31.0.50",
		"TMM_INT_SELFIP":          "10.51.0.50",
		"TMM_EXT_IFACE":           "ens5",
		"TMM_INT_IFACE":           "ens6",
		"JUMPHOST_INSTANCE_NAME":  "test-cluster-jumphost",
		"JUMPHOST_BNK_EXT_NIC_IP": "10.30.0.75",
		"GATEWAYCLASS_NAME":       "f5-gateway",
	})

	m := topology.Build(cl, st)

	if m.TmmPool.NodeName != "gke-tmm-node-1" {
		t.Errorf("TmmPool.NodeName: got %q want gke-tmm-node-1", m.TmmPool.NodeName)
	}
	if m.AgentPool.NodeName != "gke-agent-node-1" {
		t.Errorf("AgentPool.NodeName: got %q want gke-agent-node-1", m.AgentPool.NodeName)
	}
	if m.TMM.ExtSelfIP != "10.31.0.50" {
		t.Errorf("TMM.ExtSelfIP: got %q want 10.31.0.50", m.TMM.ExtSelfIP)
	}
	if m.TMM.ExtIfname != "ens5" {
		t.Errorf("TMM.ExtIfname: got %q want ens5", m.TMM.ExtIfname)
	}
	if m.Jumphost.InstanceName != "test-cluster-jumphost" {
		t.Errorf("Jumphost.InstanceName: got %q want test-cluster-jumphost", m.Jumphost.InstanceName)
	}
	if m.Jumphost.BnkExtIP != "10.30.0.75" {
		t.Errorf("Jumphost.BnkExtIP: got %q want 10.30.0.75", m.Jumphost.BnkExtIP)
	}
	if m.GatewayClassName != "f5-gateway" {
		t.Errorf("GatewayClassName: got %q want f5-gateway", m.GatewayClassName)
	}
	if m.ExternalVPC.CIDR != "10.30.0.0/24" {
		t.Errorf("ExternalVPC.CIDR: got %q want 10.30.0.0/24", m.ExternalVPC.CIDR)
	}
}

// TestBuild_EmptyStateFallsBackToIntent verifies that missing state keys fall
// back to intent-derived values.
func TestBuild_EmptyStateFallsBackToIntent(t *testing.T) {
	cl := minimalCluster()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}

	m := topology.Build(cl, st)

	if m.ClusterName != "test-cluster" {
		t.Errorf("ClusterName: got %q want test-cluster", m.ClusterName)
	}
	if m.MainVPC.Name != "bnkhd-vpc" {
		t.Errorf("MainVPC.Name: got %q want bnkhd-vpc", m.MainVPC.Name)
	}
	if m.ExternalVPC.Name != "bnkhd-ext" {
		t.Errorf("ExternalVPC.Name: got %q want bnkhd-ext", m.ExternalVPC.Name)
	}
	if m.InternalVPC.Name != "bnkhd-int" {
		t.Errorf("InternalVPC.Name: got %q want bnkhd-int", m.InternalVPC.Name)
	}
	if m.TmmPool.MachineType != "e2-standard-8" {
		t.Errorf("TmmPool.MachineType: got %q want e2-standard-8", m.TmmPool.MachineType)
	}
}

// TestBuild_MissingEverythingNoFields verifies that a minimal cluster with nil
// state produces "" in live-only fields (no panic).
func TestBuild_MissingEverythingNoFields(t *testing.T) {
	cl := minimalCluster()
	m := topology.Build(cl, nil)

	if m.TmmPool.NodeName != "" {
		t.Errorf("TmmPool.NodeName should be empty, got %q", m.TmmPool.NodeName)
	}
	if m.Jumphost.InstanceName != "" {
		t.Errorf("Jumphost.InstanceName should be empty, got %q", m.Jumphost.InstanceName)
	}
	if m.GatewayClassName != "" {
		t.Errorf("GatewayClassName should be empty, got %q", m.GatewayClassName)
	}
}

// TestEnrichFromK8s_PopulatesLivePodsAndTMM verifies live K8s pod discovery across BNK namespaces.
func TestEnrichFromK8s_PopulatesLivePodsAndTMM(t *testing.T) {
	cl := minimalCluster()
	m := topology.Build(cl, nil)

	cs := fake.NewClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "f5-tmm-abc123",
				Namespace: "f5-cne-system",
				Labels:    map[string]string{"app": "f5-tmm"},
			},
			Spec: corev1.PodSpec{
				NodeName: "gke-tmm-live-node",
			},
			Status: corev1.PodStatus{
				PodIP: "10.66.1.15",
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "f5-toda-fluentd-xyz",
				Namespace: "f5-cne-core",
			},
		},
	)

	topology.EnrichFromK8s(context.Background(), &m, cs, nil)

	if m.TMM.LivePodName != "f5-tmm-abc123" {
		t.Errorf("TMM.LivePodName = %q, want f5-tmm-abc123", m.TMM.LivePodName)
	}
	if m.TMM.LivePodIP != "10.66.1.15" {
		t.Errorf("TMM.LivePodIP = %q, want 10.66.1.15", m.TMM.LivePodIP)
	}
	if m.TmmPool.NodeName != "gke-tmm-live-node" {
		t.Errorf("TmmPool.NodeName = %q, want gke-tmm-live-node", m.TmmPool.NodeName)
	}
	if m.NamespacePods["f5-cne-system"] != 1 || m.NamespacePods["f5-cne-core"] != 1 {
		t.Errorf("NamespacePods = %v, want f5-cne-system=1 and f5-cne-core=1", m.NamespacePods)
	}
}

// TestRenderASCII_FullModel checks that a fully-populated Model contains
// expected strings in the ASCII output.
func TestRenderASCII_FullModel(t *testing.T) {
	cl := minimalCluster()
	dir := t.TempDir()
	st := writeStateEnv(t, dir, map[string]string{
		"TMM_EXT_SELFIP":          "10.31.0.50",
		"TMM_INT_SELFIP":          "10.51.0.50",
		"JUMPHOST_INSTANCE_NAME":  "test-cluster-jumphost",
		"JUMPHOST_BNK_EXT_NIC_IP": "10.30.0.5",
		"GATEWAYCLASS_NAME":       "f5-spk-gateway",
	})

	m := topology.Build(cl, st)
	out := topology.RenderASCII(m)

	checks := []string{
		"test-cluster",
		"bnkhd-vpc",
		"bnkhd-ext",
		"bnkhd-int",
		"10.31.0.50",
		"10.51.0.50",
		"10.30.0.0/24",
		"10.50.0.0/24",
		"test-cluster-jumphost",
		"10.30.0.5",
		"f5-spk-gateway",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("RenderASCII: expected %q in output\n---\n%s\n---", want, out)
		}
	}
}

// TestRenderASCII_EmptyModel verifies that an all-empty Model renders without
// panicking and includes the "(not provisioned)" sentinel.
func TestRenderASCII_EmptyModel(t *testing.T) {
	cl := &intent.Cluster{
		APIVersion: "gkebnkctl/v1",
		Kind:       "Cluster",
		Metadata: intent.Metadata{
			Name:    "empty",
			Project: "proj",
			Zone:    "us-central1-a",
		},
	}
	m := topology.Build(cl, nil)
	out := topology.RenderASCII(m)

	if !strings.Contains(out, "(not provisioned)") {
		t.Errorf("RenderASCII: expected (not provisioned) in empty-model output\n%s", out)
	}
	if !strings.Contains(out, "┌") || !strings.Contains(out, "└") {
		t.Errorf("RenderASCII: missing box-drawing characters in output\n%s", out)
	}
}

// TestRenderMermaid_GraphTDAndLabels verifies that RenderMermaid produces
// a graph TD header and includes expected node labels.
func TestRenderMermaid_GraphTDAndLabels(t *testing.T) {
	cl := minimalCluster()
	dir := t.TempDir()
	st := writeStateEnv(t, dir, map[string]string{
		"TMM_EXT_SELFIP": "10.31.0.50",
		"TMM_INT_SELFIP": "10.51.0.50",
	})
	m := topology.Build(cl, st)
	out := topology.RenderMermaid(m)

	if !strings.HasPrefix(out, "graph TD") {
		t.Errorf("RenderMermaid: expected output to start with 'graph TD', got:\n%s", out)
	}
	checks := []string{
		"10.60.0.0/24",
		"10.30.0.0/24",
		"10.50.0.0/24",
		"10.31.0.50",
		"10.51.0.50",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("RenderMermaid: expected %q in output\n---\n%s\n---", want, out)
		}
	}
}

// TestRenderDOTAndJSON verifies DOT and JSON renderers.
func TestRenderDOTAndJSON(t *testing.T) {
	cl := minimalCluster()
	m := topology.Build(cl, nil)

	dot := topology.RenderDOT(m)
	if !strings.HasPrefix(dot, "digraph G") || !strings.Contains(dot, "bnkhd-vpc") {
		t.Errorf("RenderDOT unexpected output:\n%s", dot)
	}

	js, err := topology.RenderJSON(m)
	if err != nil {
		t.Fatalf("RenderJSON error: %v", err)
	}
	if !strings.Contains(js, `"clusterName": "test-cluster"`) {
		t.Errorf("RenderJSON missing clusterName:\n%s", js)
	}
}

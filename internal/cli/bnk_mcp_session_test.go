package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s/mcpsession"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/migrate"
)

func resetMCPSessionFlags() {
	flagMCPSessionName = ""
	flagMCPSessionNamespace = "default"
	flagMCPSessionGateway = ""
	flagMCPSessionListeners = nil
	flagMCPSessionIRules = nil
	flagMCPSessionType = mcpsession.TypeMCP
	flagMCPSessionTimeout = mcpsession.DefaultTimeout
	flagMCPSessionSecret = ""
	flagMCPSessionField = mcpsession.DefaultPassphraseField
	flagMCPSessionPassphraseEnv = ""
	flagMCPSessionNetPolicy = ""
	flagMCPSessionApply = false
	flagMCPSessionWait = 0
	flagMCPSessionKubeconfig = ""
	flagMCPSessionConfig = ""
	flagOutput = "text"
}

type captureApplier struct{ objs []*unstructured.Unstructured }

func (c *captureApplier) Apply(_ context.Context, obj *unstructured.Unstructured) error {
	c.objs = append(c.objs, obj)
	return nil
}

func TestBnkMCPSessionRegistered(t *testing.T) {
	found := false
	for _, sub := range bnkCmd.Commands() {
		if sub.Name() == "mcp-session" {
			found = true
		}
	}
	if !found {
		t.Fatal("bnk mcp-session not registered")
	}
	for _, flag := range []string{"name", "namespace", "gateway", "listener", "irule", "type", "timeout", "secret", "passphrase-field", "passphrase-env", "net-policy-name", "apply", "wait", "kubeconfig", "config"} {
		if bnkMCPSessionCmd.Flags().Lookup(flag) == nil {
			t.Errorf("bnk mcp-session --%s missing", flag)
		}
	}
}

func TestBnkMCPSession_RenderYAML(t *testing.T) {
	resetMCPSessionFlags()
	t.Cleanup(resetMCPSessionFlags)
	flagMCPSessionName = "mcp-session"
	flagMCPSessionGateway = "bnk-agentcore-demo-gateway"
	flagMCPSessionListeners = []string{"http", "https"}
	flagMCPSessionIRules = []string{"mcp-rate-limit-irule"}
	flagMCPSessionTimeout = 600
	t.Setenv("MCP_SESSION_PASSPHRASE", "demo-passphrase")
	flagMCPSessionPassphraseEnv = "MCP_SESSION_PASSPHRASE"

	var out bytes.Buffer
	bnkMCPSessionCmd.SetOut(&out)
	if err := runBnkMCPSession(bnkMCPSessionCmd, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	y := out.String()
	for _, want := range []string{
		"kind: Secret", "passphrase: demo-passphrase",
		"kind: F5BigPersistenceProfile", "persistenceType: MODEL_CONTEXT_PROTOCOL", "timeout: 600", "name: mcp-session-passphrase",
		"kind: NetPolicy", "name: mcp-session-http", "name: mcp-session-https", "sectionName: https",
		"kind: F5BigCneIrule", "name: mcp-rate-limit-irule",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("yaml missing %q:\n%s", want, y)
		}
	}
	if strings.Count(y, "\n---\n") != 3 {
		t.Errorf("want 4 documents:\n%s", y)
	}
}

func TestBnkMCPSession_ValidationAndEnv(t *testing.T) {
	resetMCPSessionFlags()
	t.Cleanup(resetMCPSessionFlags)
	flagMCPSessionName = "Bad_Name"
	flagMCPSessionGateway = "gw"
	if err := runBnkMCPSession(bnkMCPSessionCmd, nil); err == nil || !strings.Contains(err.Error(), "lowercase DNS label") {
		t.Errorf("validation: err = %v", err)
	}

	flagMCPSessionName = "ok"
	flagMCPSessionPassphraseEnv = "MCP_SESSION_PASSPHRASE_UNSET_FOR_TEST"
	if err := runBnkMCPSession(bnkMCPSessionCmd, nil); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Errorf("empty env: err = %v", err)
	}
}

func TestBnkMCPSession_Apply(t *testing.T) {
	resetMCPSessionFlags()
	t.Cleanup(resetMCPSessionFlags)
	cap := &captureApplier{}
	orig := bnkMCPSessionClients
	bnkMCPSessionClients = func(string) (migrate.Applier, dynamic.Interface, error) { return cap, nil, nil }
	t.Cleanup(func() { bnkMCPSessionClients = orig })

	flagMCPSessionName = "a2a-session"
	flagMCPSessionNamespace = "agents"
	flagMCPSessionGateway = "gw"
	flagMCPSessionType = mcpsession.TypeA2A
	flagMCPSessionSecret = "shared"
	flagMCPSessionApply = true

	if err := runBnkMCPSession(bnkMCPSessionCmd, nil); err != nil {
		t.Fatal(err)
	}
	if len(cap.objs) != 2 || cap.objs[0].GetKind() != "F5BigPersistenceProfile" || cap.objs[1].GetKind() != "NetPolicy" {
		t.Fatalf("applied %d objects: %v", len(cap.objs), cap.objs)
	}
	if v, _, _ := unstructured.NestedString(cap.objs[0].Object, "spec", "a2aEncryptionPassphrase", "secretRef", "name"); v != "shared" {
		t.Errorf("a2a secretRef = %q", v)
	}
	if cap.objs[1].GetName() != "a2a-session" || cap.objs[1].GetNamespace() != "agents" {
		t.Errorf("netpolicy = %s/%s", cap.objs[1].GetNamespace(), cap.objs[1].GetName())
	}
}

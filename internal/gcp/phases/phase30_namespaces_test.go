package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

func TestPhase30Namespaces_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	// A nil *gcp.Clients (the GKEBNKCTL_SKIP_AUTH=1 dry-run path) must never
	// be dereferenced before the dry-run return.
	if err := Phase30Namespaces(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase30Namespaces(dryRun=true): %v", err)
	}
}

// TestPhase30Namespaces_LiveMode_NilClientsErrors proves the live-mode path
// fails fast (rather than nil-panicking) when Clients.Dynamic/RESTMapper
// aren't attached.
func TestPhase30Namespaces_LiveMode_NilClientsErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	err := Phase30Namespaces(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

// TestNamespacesTemplate_RendersThreeNamespaces proves the embedded template
// this phase renders parses to exactly f5-bnk, f5-utils, and the configured
// TestNamespace — a regression guard independent of the SSA apply path (which
// dynamicfake cannot exercise — see docs/SPEC-go-tracer.md §7).
func TestNamespacesTemplate_RendersThreeNamespaces(t *testing.T) {
	cl := testCluster()
	tmplBytes, err := manifests.FS.ReadFile(namespacesTemplatePath)
	if err != nil {
		t.Fatalf("reading template: %v", err)
	}
	rendered, err := render.RenderNamespaces(tmplBytes, render.NamespacesVars{
		TestNamespace: cl.Egress.TestNamespace,
	})
	if err != nil {
		t.Fatalf("rendering template: %v", err)
	}
	objs, err := k8s.ParseYAMLStream(rendered)
	if err != nil {
		t.Fatalf("parsing rendered template: %v", err)
	}
	names := make([]string, 0)
	for _, o := range objs {
		if o.GetKind() == "Namespace" {
			names = append(names, o.GetName())
		}
	}
	want := map[string]bool{"f5-bnk": false, "f5-utils": false, "egress-test": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected namespace %s in rendered template, got names=%v", name, names)
		}
	}
}

// TestNamespacesTemplate_CustomTestNamespace proves the template correctly
// renders a custom egress test namespace when configured.
func TestNamespacesTemplate_CustomTestNamespace(t *testing.T) {
	customNamespace := "custom-egress-ns"
	tmplBytes, err := manifests.FS.ReadFile(namespacesTemplatePath)
	if err != nil {
		t.Fatalf("reading template: %v", err)
	}
	rendered, err := render.RenderNamespaces(tmplBytes, render.NamespacesVars{
		TestNamespace: customNamespace,
	})
	if err != nil {
		t.Fatalf("rendering template: %v", err)
	}
	objs, err := k8s.ParseYAMLStream(rendered)
	if err != nil {
		t.Fatalf("parsing rendered template: %v", err)
	}
	var found bool
	for _, o := range objs {
		if o.GetKind() == "Namespace" && o.GetName() == customNamespace {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected namespace %s in rendered template, but it was not found", customNamespace)
	}
}

package phases

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

// staticRouteGVR must match what the fake dynamic client derives from the
// Kind (meta.UnsafeGuessKindToResource: "F5SPKStaticRoute" ->
// "f5spkstaticroutes"), otherwise seeded objects are stored under a different
// GVR than the code lists. The live cluster's real resource name is
// "f5-spk-staticroutes"; production resolves it through the discovery-backed
// RESTMapper, which the test mapper below stands in for.
var staticRouteGVR = schema.GroupVersionResource{
	Group: "k8s.f5net.com", Version: "v1", Resource: "f5spkstaticroutes",
}

// newStaticRoute builds an F5SPKStaticRoute the way the live API returns one:
// prefixLen typed as int64.
func newStaticRoute(name, dest string, prefixLen int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "k8s.f5net.com/v1",
		"kind":       staticRouteKind,
		"metadata":   map[string]interface{}{"name": name, "namespace": "f5-bnk"},
		"spec": map[string]interface{}{
			"destination": dest,
			"prefixLen":   prefixLen,
			"gateway":     "10.0.64.1",
			"type":        "gateway",
		},
	}}
}

// driftTestClients wires a fake dynamic client to a RESTMapper that resolves
// F5SPKStaticRoute, mirroring what AttachK8s builds live.
func driftTestClients(objs ...runtime.Object) *gcp.Clients {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		staticRouteGVR: "F5SPKStaticRouteList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)

	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{staticRouteGVR.GroupVersion()})
	mapper.AddSpecific(
		schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: staticRouteKind},
		staticRouteGVR,
		schema.GroupVersionResource{Group: "k8s.f5net.com", Version: "v1", Resource: "f5-spk-staticroute"},
		meta.RESTScopeNamespace,
	)
	return &gcp.Clients{Dynamic: dyn, RESTMapper: mapper}
}

func routeNames(t *testing.T, clients *gcp.Clients) []string {
	t.Helper()
	list, err := clients.Dynamic.Resource(staticRouteGVR).Namespace("f5-bnk").
		List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing routes: %v", err)
	}
	var names []string
	for _, it := range list.Items {
		names = append(names, it.GetName())
	}
	return names
}

// A hand-applied route squatting on a destination we declare is the case that
// hard-fails the apply against BNK's uniqueness webhook. It must be deleted so
// the declared route can take the destination.
func TestReconcileStaticRouteDrift_DeletesConflictingName(t *testing.T) {
	clients := driftTestClients(newStaticRoute("route-egress-pods-ingress-vxlan", "10.66.4.0", 24))
	desired := []*unstructured.Unstructured{newStaticRoute("route-ingress-vxlan-10-66-4-0", "10.66.4.0", 24)}

	if err := reconcileStaticRouteDrift(context.Background(), clients, desired); err != nil {
		t.Fatalf("reconcileStaticRouteDrift: %v", err)
	}
	if got := routeNames(t, clients); len(got) != 0 {
		t.Errorf("drifted route not deleted, still present: %v", got)
	}
}

// The idempotent re-run: the route occupying the destination IS the one we
// declare. Deleting it would cause a needless traffic interruption on every
// `up`, so it must be left in place for server-side apply to update.
func TestReconcileStaticRouteDrift_KeepsOwnRoute(t *testing.T) {
	clients := driftTestClients(newStaticRoute("route-ingress-vxlan-10-66-4-0", "10.66.4.0", 24))
	desired := []*unstructured.Unstructured{newStaticRoute("route-ingress-vxlan-10-66-4-0", "10.66.4.0", 24)}

	if err := reconcileStaticRouteDrift(context.Background(), clients, desired); err != nil {
		t.Fatalf("reconcileStaticRouteDrift: %v", err)
	}
	got := routeNames(t, clients)
	if len(got) != 1 || got[0] != "route-ingress-vxlan-10-66-4-0" {
		t.Errorf("own route should survive untouched, got %v", got)
	}
}

// Blast-radius guard: routes on other destinations are never touched. Phase41's
// cluster-wide route-cluster-pods (10.66.0.0/16) must survive Phase51 declaring
// a per-node 10.66.4.0/24 — same supernet, different prefix, not a conflict.
func TestReconcileStaticRouteDrift_LeavesUnrelatedRoutes(t *testing.T) {
	clients := driftTestClients(
		newStaticRoute("route-cluster-pods", "10.66.0.0", 16),
		newStaticRoute("route-ext-gw", "0.0.0.0", 0),
		newStaticRoute("route-int-gw", "10.51.0.0", 24),
	)
	desired := []*unstructured.Unstructured{newStaticRoute("route-ingress-vxlan-10-66-4-0", "10.66.4.0", 24)}

	if err := reconcileStaticRouteDrift(context.Background(), clients, desired); err != nil {
		t.Fatalf("reconcileStaticRouteDrift: %v", err)
	}
	if got := routeNames(t, clients); len(got) != 3 {
		t.Errorf("unrelated routes must survive, got %v", got)
	}
}

// Greenfield: nothing exists yet, nothing to reconcile, no error.
func TestReconcileStaticRouteDrift_Greenfield(t *testing.T) {
	clients := driftTestClients()
	desired := []*unstructured.Unstructured{newStaticRoute("route-ingress-vxlan-10-66-4-0", "10.66.4.0", 24)}

	if err := reconcileStaticRouteDrift(context.Background(), clients, desired); err != nil {
		t.Fatalf("reconcileStaticRouteDrift on empty cluster: %v", err)
	}
}

// Phase41 renders F5SPKVlans alongside its routes; non-route objects must not
// drive any List/Delete traffic.
func TestReconcileStaticRouteDrift_IgnoresNonRouteKinds(t *testing.T) {
	clients := driftTestClients(newStaticRoute("route-cluster-pods", "10.66.0.0", 16))
	vlan := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "k8s.f5net.com/v1",
		"kind":       "F5SPKVlan",
		"metadata":   map[string]interface{}{"name": "external", "namespace": "f5-bnk"},
		"spec":       map[string]interface{}{"destination": "10.66.0.0", "prefixLen": int64(16)},
	}}

	if err := reconcileStaticRouteDrift(context.Background(), clients, []*unstructured.Unstructured{vlan}); err != nil {
		t.Fatalf("reconcileStaticRouteDrift with only non-route objects: %v", err)
	}
	if got := routeNames(t, clients); len(got) != 1 {
		t.Errorf("a same-destination F5SPKVlan must not cause a route deletion, got %v", got)
	}
}

// A rendered manifest round-tripped through JSON types prefixLen as float64,
// while the live API returns int64. Both must produce the same identity or
// drift silently goes undetected.
func TestStaticRouteDestKey_NumericTypesAgree(t *testing.T) {
	fromAPI := newStaticRoute("a", "10.66.4.0", 24)
	fromManifest := &unstructured.Unstructured{Object: map[string]interface{}{
		"kind":     staticRouteKind,
		"metadata": map[string]interface{}{"name": "b", "namespace": "f5-bnk"},
		"spec":     map[string]interface{}{"destination": "10.66.4.0", "prefixLen": float64(24)},
	}}

	got1, ok1 := staticRouteDestKey(fromAPI)
	got2, ok2 := staticRouteDestKey(fromManifest)
	if !ok1 || !ok2 {
		t.Fatalf("both forms must yield a key, got ok=%v,%v", ok1, ok2)
	}
	if got1 != got2 {
		t.Errorf("int64 key %q != float64 key %q", got1, got2)
	}
}

func TestStaticRouteDestKey_NoDestination(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"kind":     staticRouteKind,
		"metadata": map[string]interface{}{"name": "v6-only"},
		"spec":     map[string]interface{}{"destination": "", "prefixLen": int64(24)},
	}}
	if _, ok := staticRouteDestKey(obj); ok {
		t.Error("a route with no IPv4 destination must not produce a conflict key")
	}
}

func TestReconcileStaticRouteDrift_NilClientsErrors(t *testing.T) {
	desired := []*unstructured.Unstructured{newStaticRoute("route-ingress-vxlan-10-66-4-0", "10.66.4.0", 24)}
	err := reconcileStaticRouteDrift(context.Background(), &gcp.Clients{}, desired)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic/RESTMapper are nil")
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

package k8s

import (
	"bytes"
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/yaml"
)

// FieldManager is the field-manager string used for every server-side apply
// call — distinct from kubectl's own field managers so an SSA conflict tells
// the operator gkebnkctl owns the field.
const FieldManager = "gkebnkctl"

// RESTMappingWithRefresh resolves gvk to a RESTMapping, retrying ONCE against
// a freshly-discovered API surface if the kind is initially unknown.
//
// This is load-bearing on a greenfield `up`. The RESTMapper is built once by
// AttachK8s (right after Phase20) and caches a snapshot of the API surface as
// it looked then. Later phases install CRDs into that same cluster —
// cert-manager's in Phase31, FLO's ~23 CRDs in Phase36 — and then immediately
// apply custom resources against them (Phase32's ClusterIssuer, Phase38's
// CNEInstance, Phase41/51's F5SPKStaticRoutes). Those kinds are absent from
// the snapshot.
//
// DeferredDiscoveryRESTMapper only re-discovers on a miss when its cache
// reports !Fresh(), and every cache implementation (including client-go's
// memory cache, whose Fresh() returns cacheValid) reports Fresh()==true once
// discovery has succeeded — so the built-in retry never fires. The mapper must
// be Reset() explicitly, which is what this does.
//
// Without it, a from-scratch `up` dies at Phase32 with
// `no matches for kind "ClusterIssuer" in version "cert-manager.io/v1"`
// (live-hit 2026-07-21 on cluster bnk-scratch). Re-running `up` masked the bug,
// because a second process re-discovers at startup and sees the CRDs.
//
// The retry is bounded to one attempt: a genuinely unknown kind returns the
// original NoMatch error rather than looping.
func RESTMappingWithRefresh(mapper meta.RESTMapper, gvk schema.GroupVersionKind) (*meta.RESTMapping, error) {
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err == nil || !meta.IsNoMatchError(err) {
		return mapping, err
	}
	deferred, ok := mapper.(*restmapper.DeferredDiscoveryRESTMapper)
	if !ok {
		return nil, err
	}
	deferred.Reset()
	return mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
}

// Applier server-side-applies unstructured objects against a live cluster.
// Construct one per invocation via NewApplier — not safe for concurrent
// reuse across goroutines (matches the dynamic client / RESTMapper's own
// contract).
type Applier struct {
	dyn    dynamic.Interface
	mapper meta.RESTMapper
}

// NewApplier builds a dynamic client + deferred-discovery RESTMapper from
// cfg. gkebnkctl has no `k apply` CLI surface (unlike awsbnkctl) — phases
// call Applier directly against rendered/embedded manifest bytes.
func NewApplier(cfg *rest.Config) (*Applier, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("apply: dynamic client: %w", err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("apply: discovery client: %w", err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(NewNoopCachedDiscovery(disc))
	return &Applier{dyn: dyn, mapper: mapper}, nil
}

// NewApplierFromInterfaces builds an Applier directly from an
// already-constructed dynamic client + RESTMapper — used by the BNK-install
// phases (Task C), whose *gcp.Clients bundle already holds Dynamic +
// RESTMapper (built once by gcp.Clients.AttachK8s after Phase20), and by
// phase tests that inject a dynamicfake client + a fake RESTMapper.
func NewApplierFromInterfaces(dyn dynamic.Interface, mapper meta.RESTMapper) *Applier {
	return &Applier{dyn: dyn, mapper: mapper}
}

// Apply server-side-applies a single unstructured object. force toggles
// SSA's force-conflicts flag (required for FLO's server-side apply per
// docs/SPEC-go-tracer.md §3 Phase36 — the embedded manifest is large enough
// that annotation-size forces SSA, and re-applies commonly need force).
func (a *Applier) Apply(ctx context.Context, obj *unstructured.Unstructured, force bool) (*unstructured.Unstructured, error) {
	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" {
		return nil, fmt.Errorf("apply: resource missing kind: %s", obj.GetName())
	}
	mapping, err := RESTMappingWithRefresh(a.mapper, gvk)
	if err != nil {
		return nil, fmt.Errorf("apply: REST mapping for %s: %w", gvk, err)
	}

	ns := obj.GetNamespace()
	var ri dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if ns == "" {
			ns = "default"
		}
		obj.SetNamespace(ns)
		ri = a.dyn.Resource(mapping.Resource).Namespace(ns)
	} else {
		ri = a.dyn.Resource(mapping.Resource)
	}

	body, err := yaml.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("apply: marshaling %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}

	po := metav1.PatchOptions{FieldManager: FieldManager}
	if force {
		t := true
		po.Force = &t
	}
	applied, err := ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, body, po)
	if err != nil {
		return nil, fmt.Errorf("apply: server-side apply %s %s/%s: %w",
			mapping.Resource.Resource, ns, obj.GetName(), err)
	}
	return applied, nil
}

// ApplyAll applies each object in objs in order, stopping at the first
// error. Phases apply multi-document manifests (e.g. the four Multus bundle
// files) with this in strict file order.
func (a *Applier) ApplyAll(ctx context.Context, objs []*unstructured.Unstructured, force bool) error {
	for _, obj := range objs {
		if _, err := a.Apply(ctx, obj, force); err != nil {
			return err
		}
	}
	return nil
}

// ParseYAMLStream splits a multi-document YAML byte stream (as read from an
// embedded manifest file) into individual unstructured objects. Empty
// documents are skipped.
func ParseYAMLStream(data []byte) ([]*unstructured.Unstructured, error) {
	parts := SplitYAMLDocs(data)
	out := make([]*unstructured.Unstructured, 0, len(parts))
	for _, doc := range parts {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal(doc, &u.Object); err != nil {
			return nil, fmt.Errorf("apply: parsing yaml: %w", err)
		}
		if len(u.Object) == 0 {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// NewNoopCachedDiscovery adapts a discovery.DiscoveryInterface into a
// CachedDiscoveryInterface (no actual caching — fine for one-shot CLI runs).
func NewNoopCachedDiscovery(delegate discovery.DiscoveryInterface) discovery.CachedDiscoveryInterface {
	return memCacheClient{DiscoveryInterface: delegate}
}

type memCacheClient struct{ discovery.DiscoveryInterface }

func (memCacheClient) Fresh() bool { return true }
func (memCacheClient) Invalidate() {}

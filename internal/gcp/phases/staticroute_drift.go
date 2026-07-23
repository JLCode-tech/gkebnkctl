package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

const (
	staticRouteKind = "F5SPKStaticRoute"

	// staticRouteGoneTimeout bounds the wait for a drifted route to actually
	// disappear after Delete. F5SPKStaticRoutes carry the
	// `handletmmconfig_inconsistency` finalizer, so deletion is asynchronous:
	// the CR lingers in Terminating until the CNE controller has withdrawn the
	// route from TMM. Re-applying before it is gone would still trip the
	// uniqueness webhook, so this wait is load-bearing, not cosmetic.
	staticRouteGoneTimeout = 90 * time.Second
	staticRouteGonePoll    = 2 * time.Second
)

// reconcileStaticRouteDrift makes gkebnkctl authoritative over the static
// routes it declares.
//
// BNK's `f5validate.f5net.com` admission webhook enforces a unique
// destination+prefixLen across ALL F5SPKStaticRoutes in the cluster,
// *regardless of CR name*:
//
//	CR route-ingress-vxlan-10-66-4-0 cannot be created or updated with an
//	already existing staticroute destination address 10.66.4.0 with prefixLen 24
//
// So a hand-applied route occupying a destination this tool declares does not
// merely coexist — it hard-blocks the apply and fails the whole `up` run.
// Rather than skip or error out, we treat the declared manifest as the source
// of truth and delete the conflicting CR, then re-create it under the
// tool-managed name. That keeps three cases working with one code path:
//
//   - greenfield: no conflict, nothing deleted, plain apply.
//   - idempotent re-run: the conflicting route IS ours (same name), so it is
//     left alone and server-side apply updates it in place. Verified live on
//     cluster bnk-hostdev 2026-07-21 — the webhook accepts a same-name
//     re-apply, and only rejects a *different* CR claiming a taken destination.
//   - drift: an operator hand-applied the route (or an older/renamed release
//     of this tool did); it is replaced by the declared one.
//
// Scope is deliberately narrow: a route is only deleted when its
// destination+prefixLen matches EXACTLY one this manifest is about to create
// under a different name. Routes covering other destinations are never
// touched — notably Phase41's cluster-wide route-cluster-pods (10.66.0.0/16)
// is not a conflict for Phase51's per-node 10.66.4.0/24, since the prefix
// differs.
//
// Replacing a live route is briefly traffic-affecting: between the delete and
// the re-apply, TMM has no route for that destination. That window is
// unavoidable (the webhook forbids create-before-delete), so it is logged
// loudly rather than hidden.
func reconcileStaticRouteDrift(ctx context.Context, clients *gcp.Clients, objs []*unstructured.Unstructured) error {
	// desired maps namespace -> "<destination>/<prefixLen>" -> declared name.
	desired := map[string]map[string]string{}
	for _, obj := range objs {
		if obj.GetKind() != staticRouteKind {
			continue
		}
		key, ok := staticRouteDestKey(obj)
		if !ok {
			continue
		}
		ns := obj.GetNamespace()
		if desired[ns] == nil {
			desired[ns] = map[string]string{}
		}
		desired[ns][key] = obj.GetName()
	}
	if len(desired) == 0 {
		return nil
	}

	if clients == nil || clients.Dynamic == nil || clients.RESTMapper == nil {
		return fmt.Errorf("Clients.Dynamic/RESTMapper is nil — AttachK8s must run first (after Phase20)")
	}
	gvk := schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: staticRouteKind}
	// Must refresh discovery on a miss: F5SPKStaticRoute's CRD is installed by
	// Phase36 (FLO), long after AttachK8s snapshotted the API surface, so on a
	// greenfield run the kind is absent from the mapper's cache.
	mapping, err := k8s.RESTMappingWithRefresh(clients.RESTMapper, gvk)
	if err != nil {
		return fmt.Errorf("REST mapping for %s: %w", gvk, err)
	}

	for ns, want := range desired {
		ri := clients.Dynamic.Resource(mapping.Resource).Namespace(ns)
		existing, err := ri.List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("listing %s in %s: %w", mapping.Resource.Resource, ns, err)
		}
		for i := range existing.Items {
			item := &existing.Items[i]
			key, ok := staticRouteDestKey(item)
			if !ok {
				continue
			}
			declaredName, conflicts := want[key]
			if !conflicts || declaredName == item.GetName() {
				// Not a destination we declare, or it is already the CR we
				// are about to apply — server-side apply handles that.
				continue
			}
			fmt.Fprintf(os.Stderr,
				"[drift] %s/%s occupies %s, which %s declares — deleting it so the declared route can be applied (ingress to that destination is briefly interrupted)\n",
				ns, item.GetName(), key, declaredName)
			if err := ri.Delete(ctx, item.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting drifted route %s/%s: %w", ns, item.GetName(), err)
			}
			if err := waitStaticRouteGone(ctx, clients, mapping.Resource, ns, item.GetName()); err != nil {
				return err
			}
		}
	}
	return nil
}

// staticRouteDestKey builds the "<destination>/<prefixLen>" identity the
// uniqueness webhook keys on. Returns ok=false when the object has no IPv4
// destination (nothing to conflict on). Only the v4 destination is
// considered: destination_v6/prefixLen_v6 are defaulted on every CR
// (prefixLen_v6: 120) and this tool declares no v6 routes, so keying on them
// would manufacture false conflicts between unrelated routes.
func staticRouteDestKey(obj *unstructured.Unstructured) (string, bool) {
	dest, found, err := unstructured.NestedString(obj.Object, "spec", "destination")
	if err != nil || !found || dest == "" {
		return "", false
	}
	prefixLen, ok := nestedNumber(obj.Object, "spec", "prefixLen")
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s/%d", dest, prefixLen), true
}

// nestedNumber reads an integer field that may be typed as either int64 (the
// live API's unstructured decoding) or float64 (JSON round-tripping of a
// rendered manifest), so drift detection compares equal in both directions.
func nestedNumber(obj map[string]interface{}, fields ...string) (int64, bool) {
	v, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if err != nil || !found {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

// waitStaticRouteGone blocks until name is actually absent from the API (not
// merely marked for deletion). See staticRouteGoneTimeout's comment for why
// the finalizer makes this necessary.
func waitStaticRouteGone(ctx context.Context, clients *gcp.Clients, gvr schema.GroupVersionResource, ns, name string) error {
	ri := clients.Dynamic.Resource(gvr).Namespace(ns)
	deadline := time.Now().Add(staticRouteGoneTimeout)
	for {
		_, err := ri.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("waiting for %s/%s to be deleted: %w", ns, name, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for drifted route %s/%s to finish deleting (finalizer %q still held?)",
				staticRouteGoneTimeout, ns, name, "handletmmconfig_inconsistency")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(staticRouteGonePoll):
		}
	}
}

// applyStaticRouteManifestBytes is applyManifestBytes plus a
// reconcileStaticRouteDrift pass over the parsed objects, for the two phases
// that declare F5SPKStaticRoutes (Phase41, Phase51). Phases that declare no
// static routes keep using applyManifestBytes — the drift pass is a no-op for
// them anyway, but not paying for a List on every apply keeps the blast
// radius and the API traffic tied to the phases that actually need it.
func applyStaticRouteManifestBytes(ctx context.Context, clients *gcp.Clients, data []byte, force bool) error {
	applier, err := applierFor(clients)
	if err != nil {
		return err
	}
	objs, err := k8s.ParseYAMLStream(data)
	if err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}
	if err := reconcileStaticRouteDrift(ctx, clients, objs); err != nil {
		return err
	}
	return applier.ApplyAll(ctx, objs, force)
}

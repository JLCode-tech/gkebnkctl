package phases

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
)

// applierFor builds a k8s.Applier from clients.Dynamic + clients.RESTMapper,
// which gcp.Clients.AttachK8s populates once (after Phase20 — the GKE
// cluster must be RUNNING). Every phase 30-42 real-work branch calls this
// AFTER its own dry-run check, so a nil clients (GKEBNKCTL_SKIP_AUTH=1) never
// reaches here outside --dry-run (see docs/SPEC-go-tracer.md §6).
func applierFor(clients *gcp.Clients) (*k8s.Applier, error) {
	if clients == nil || clients.Dynamic == nil || clients.RESTMapper == nil {
		return nil, fmt.Errorf("Clients.Dynamic/RESTMapper is nil — AttachK8s must run first (after Phase20)")
	}
	return k8s.NewApplierFromInterfaces(clients.Dynamic, clients.RESTMapper), nil
}

// applyManifestBytes parses a multi-document YAML byte stream and
// server-side-applies every object in file order via applierFor(clients).
func applyManifestBytes(ctx context.Context, clients *gcp.Clients, data []byte, force bool) error {
	applier, err := applierFor(clients)
	if err != nil {
		return err
	}
	objs, err := k8s.ParseYAMLStream(data)
	if err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}
	return applier.ApplyAll(ctx, objs, force)
}

// applyObjects server-side-applies already-parsed objects. Used by Phase36,
// which must filter the vendored FLO manifest (dropping Secrets whose data was
// redacted before commit) before applying rather than streaming it verbatim.
func applyObjects(ctx context.Context, clients *gcp.Clients, objs []*unstructured.Unstructured, force bool) error {
	applier, err := applierFor(clients)
	if err != nil {
		return err
	}
	return applier.ApplyAll(ctx, objs, force)
}

// applyEmbeddedManifest reads path from the embedded manifests.FS and
// applies it via applyManifestBytes.
func applyEmbeddedManifest(ctx context.Context, clients *gcp.Clients, path string, force bool) error {
	data, err := manifests.FS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading embedded manifest %s: %w", path, err)
	}
	if err := applyManifestBytes(ctx, clients, data, force); err != nil {
		return fmt.Errorf("applying %s: %w", path, err)
	}
	return nil
}

// applyEmbeddedManifests applies each path in paths, in order, stopping at
// the first error. Used by Phase34Multus, whose four bundle files MUST apply
// in a fixed order (rbac -> crd -> configmap -> ds — the DS alone fails
// without the rest, per reference/bash-seed/manifests/02-multus-rbac.yaml's
// provenance comment).
func applyEmbeddedManifests(ctx context.Context, clients *gcp.Clients, paths []string, force bool) error {
	for _, p := range paths {
		if err := applyEmbeddedManifest(ctx, clients, p, force); err != nil {
			return err
		}
	}
	return nil
}

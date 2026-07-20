package phases

import (
	"context"
	"fmt"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
)

// bnkFieldManager is the field-manager string used by every dynamic SSA call
// in the BNK-install phases (30-42) — matches k8s.FieldManager, kept as a
// local alias so this file's intent is self-documenting without a jump.
const bnkFieldManager = k8s.FieldManager

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

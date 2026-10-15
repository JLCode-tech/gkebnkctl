package phases

import (
	"context"
	"fmt"
	"os"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// namespacesTemplatePath is the embedded template applied by Phase30Namespaces,
// rendering standard F5 BNK 2.4 namespaces and the configurable egress-test namespace.
const namespacesTemplatePath = "templates/namespaces.yaml.tmpl"
const coreDNSForwarderPath = "static/03-coredns-forwarder.yaml"

// Phase30Namespaces applies the standard F5 BNK 2.4 / egress-test namespaces
// and the CoreDNS forwarder for StatefulSet discovery.
func Phase30Namespaces(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 30] namespaces: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 30] would apply namespaces: cert-manager, f5-cne-core, f5-cne-system, f5-bnk-instance, f5-utils, %s\n", cl.Egress.TestNamespace)
		return nil
	}

	tmplBytes, err := manifests.FS.ReadFile(namespacesTemplatePath)
	if err != nil {
		return fmt.Errorf("phase30: reading embedded template: %w", err)
	}
	rendered, err := render.RenderNamespaces(tmplBytes, render.NamespacesVars{
		TestNamespace: cl.Egress.TestNamespace,
	})
	if err != nil {
		return fmt.Errorf("phase30: rendering namespaces: %w", err)
	}
	if err := applyManifestBytes(ctx, clients, rendered, false); err != nil {
		return fmt.Errorf("phase30: %w", err)
	}

	if err := applyCoreDNSForwarder(ctx, clients); err != nil {
		fmt.Fprintf(os.Stderr, "[phase 30] warn: coredns forwarder: %v\n", err)
	}

	fmt.Fprintln(os.Stderr, "[phase 30] namespaces OK")
	return nil
}

func applyCoreDNSForwarder(ctx context.Context, clients *gcp.Clients) error {
	fwdBytes, err := manifests.FS.ReadFile(coreDNSForwarderPath)
	if err != nil {
		return fmt.Errorf("reading coredns-forwarder manifest: %w", err)
	}
	if err := applyManifestBytes(ctx, clients, fwdBytes, true); err != nil {
		return fmt.Errorf("applying coredns-forwarder: %w", err)
	}

	if clients.K8s != nil {
		svc, err := clients.K8s.CoreV1().Services("kube-system").Get(ctx, "coredns-f5", metav1.GetOptions{})
		if err == nil && svc.Spec.ClusterIP != "" {
			cm, err := clients.K8s.CoreV1().ConfigMaps("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
			if err == nil {
				if cm.Data == nil {
					cm.Data = map[string]string{}
				}
				stub := fmt.Sprintf(`{"f5-cne-core.svc.cluster.local":["%s"]}`, svc.Spec.ClusterIP)
				if cm.Data["stubDomains"] != stub {
					cm.Data["stubDomains"] = stub
					_, _ = clients.K8s.CoreV1().ConfigMaps("kube-system").Update(ctx, cm, metav1.UpdateOptions{})
				}
			}
		}
	}
	return nil
}

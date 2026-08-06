package phases

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

// farSecretName is the Secret name every namespace in
// bnk.farSecret.namespaces gets, regardless of source.
const farSecretName = "far-secret"

// Phase33FarSecret provisions the far-secret pull credential into every
// namespace in cl.Bnk.FarSecret.Namespaces, per cl.Bnk.FarSecret.Source:
//
//   - "kubecontext": copy the Secret named far-secret out of
//     SourceNamespace on the local kubeconfig context SourceContext (an
//     OPERATOR-accessible cluster — not the primary GKE cluster; this is the
//     one legitimate use of a local kubeconfig in gkebnkctl, see
//     internal/k8s/client.go's package doc).
//   - "dockerconfig": build a kubernetes.io/dockerconfigjson Secret from the
//     local DockerConfigFile.
//
// Idempotent per-namespace: Secrets(ns).Get(far-secret) -> skip if present
// (docs/SPEC-go-tracer.md §3 Phase33).
func Phase33FarSecret(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 33] far-secret (source=%s): cluster=%s\n", cl.Bnk.FarSecret.Source, cl.Metadata.Name)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 33] would provision far-secret (source=%s) into namespaces %v\n",
			cl.Bnk.FarSecret.Source, cl.Bnk.FarSecret.Namespaces)
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase33: Clients.K8s is nil — Phase20 must attach the k8s client first")
	}

	switch cl.Bnk.FarSecret.Source {
	case "kubecontext":
		srcCfg, err := k8s.BuildRESTConfigFromKubeconfig("", cl.Bnk.FarSecret.SourceContext)
		if err != nil {
			return fmt.Errorf("phase33: building rest.Config for source context %s: %w", cl.Bnk.FarSecret.SourceContext, err)
		}
		srcClient, err := k8s.NewClientsetFromConfig(srcCfg)
		if err != nil {
			return fmt.Errorf("phase33: building clientset for source context %s: %w", cl.Bnk.FarSecret.SourceContext, err)
		}
		for _, ns := range cl.Bnk.FarSecret.Namespaces {
			if err := copyFarSecretFromContext(ctx, srcClient, cl.Bnk.FarSecret.SourceNamespace, clients.K8s, ns); err != nil {
				return fmt.Errorf("phase33: copying far-secret into %s: %w", ns, err)
			}
		}

	case "dockerconfig":
		dockerCfg, err := os.ReadFile(cl.Bnk.FarSecret.DockerConfigFile) // #nosec G304 -- path is operator-supplied via cluster.yaml, existence validated at intent.Load
		if err != nil {
			return fmt.Errorf("phase33: reading dockerConfigFile %s: %w", cl.Bnk.FarSecret.DockerConfigFile, err)
		}
		for _, ns := range cl.Bnk.FarSecret.Namespaces {
			if err := createFarSecretFromDockerConfig(ctx, clients.K8s, ns, dockerCfg); err != nil {
				return fmt.Errorf("phase33: creating far-secret in %s: %w", ns, err)
			}
		}

	default:
		return fmt.Errorf("phase33: unknown bnk.farSecret.source %q (want kubecontext|dockerconfig)", cl.Bnk.FarSecret.Source)
	}

	fmt.Fprintln(os.Stderr, "[phase 33] far-secret OK")
	return nil
}

// farSecretExists reports whether ns already has a far-secret Secret —
// the skip-if-exists idempotency gate shared by both source paths.
func farSecretExists(ctx context.Context, client kubernetes.Interface, ns string) (bool, error) {
	_, err := client.CoreV1().Secrets(ns).Get(ctx, farSecretName, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if k8serrors.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// copyFarSecretFromContext copies the far-secret Secret from srcNs on
// srcClient into dstNs on dstClient, stripping the server-set metadata
// fields the bash seed also strips (uid, resourceVersion,
// creationTimestamp, namespace, annotations, managedFields — see
// reference/bash-seed/bin/30-bnk-install.sh's bnk::copy_far_secret_from_context).
// Skips if dstNs already has a far-secret.
func copyFarSecretFromContext(ctx context.Context, srcClient kubernetes.Interface, srcNs string, dstClient kubernetes.Interface, dstNs string) error {
	exists, err := farSecretExists(ctx, dstClient, dstNs)
	if err != nil {
		return fmt.Errorf("checking far-secret in %s: %w", dstNs, err)
	}
	if exists {
		fmt.Fprintf(os.Stderr, "[phase 33] far-secret already present in %s — skipping\n", dstNs)
		return nil
	}

	src, err := srcClient.CoreV1().Secrets(srcNs).Get(ctx, farSecretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting source far-secret %s/%s: %w", srcNs, farSecretName, err)
	}

	fresh := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      farSecretName,
			Namespace: dstNs,
			Labels:    src.Labels,
		},
		Type: src.Type,
		Data: src.Data,
	}
	fmt.Fprintf(os.Stderr, "[phase 33] copying far-secret from %s/%s into %s\n", srcNs, farSecretName, dstNs)
	if _, err := dstClient.CoreV1().Secrets(dstNs).Create(ctx, fresh, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating far-secret in %s: %w", dstNs, err)
	}
	return nil
}

// createFarSecretFromDockerConfig builds a kubernetes.io/dockerconfigjson
// Secret in ns from raw docker-config JSON bytes. Skips if ns already has a
// far-secret.
func createFarSecretFromDockerConfig(ctx context.Context, client kubernetes.Interface, ns string, dockerConfigJSON []byte) error {
	exists, err := farSecretExists(ctx, client, ns)
	if err != nil {
		return fmt.Errorf("checking far-secret in %s: %w", ns, err)
	}
	if exists {
		fmt.Fprintf(os.Stderr, "[phase 33] far-secret already present in %s — skipping\n", ns)
		return nil
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: ns},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: dockerConfigJSON},
	}
	fmt.Fprintf(os.Stderr, "[phase 33] creating far-secret (dockerconfigjson) in %s\n", ns)
	if _, err := client.CoreV1().Secrets(ns).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating far-secret in %s: %w", ns, err)
	}
	return nil
}

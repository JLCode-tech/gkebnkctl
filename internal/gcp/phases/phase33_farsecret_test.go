package phases

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase33FarSecret_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase33FarSecret(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase33FarSecret(dryRun=true): %v", err)
	}
}

func TestPhase33FarSecret_LiveMode_NilK8sErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	err := Phase33FarSecret(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.K8s is nil in live mode")
	}
}

func TestPhase33FarSecret_UnknownSourceErrors(t *testing.T) {
	cl := testCluster()
	cl.Bnk.FarSecret.Source = "bogus"
	st := testState(t)
	clients := &gcp.Clients{K8s: fakek8s.NewClientset()}
	if err := Phase33FarSecret(context.Background(), cl, st, clients, false); err == nil {
		t.Fatal("expected an error for an unknown farSecret.source")
	}
}

func TestPhase33FarSecret_DockerConfig_CreatesInAllNamespaces(t *testing.T) {
	cl := testCluster()
	cl.Bnk.FarSecret.Source = "dockerconfig"
	cl.Bnk.FarSecret.DockerConfigFile = writeTempFile(t, `{"auths":{"repo.f5.com":{"auth":"Zm9vOmJhcg=="}}}`)
	cl.Bnk.FarSecret.Namespaces = []string{"f5-cne-core", "f5-utils"}
	st := testState(t)
	k8sClient := fakek8s.NewClientset()
	clients := &gcp.Clients{K8s: k8sClient}

	if err := Phase33FarSecret(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase33FarSecret: %v", err)
	}

	for _, ns := range cl.Bnk.FarSecret.Namespaces {
		sec, err := k8sClient.CoreV1().Secrets(ns).Get(context.Background(), farSecretName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("getting far-secret in %s: %v", ns, err)
		}
		if sec.Type != corev1.SecretTypeDockerConfigJson {
			t.Errorf("far-secret in %s: type = %s, want %s", ns, sec.Type, corev1.SecretTypeDockerConfigJson)
		}
		if len(sec.Data[corev1.DockerConfigJsonKey]) == 0 {
			t.Errorf("far-secret in %s: missing .dockerconfigjson data", ns)
		}
	}
}

// TestCreateFarSecretFromDockerConfig_SkipsWhenPresent proves the
// skip-if-exists idempotency gate.
func TestCreateFarSecretFromDockerConfig_SkipsWhenPresent(t *testing.T) {
	k8sClient := fakek8s.NewClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: "f5-cne-core"}},
	)
	if err := createFarSecretFromDockerConfig(context.Background(), k8sClient, "f5-cne-core", []byte(`{}`)); err != nil {
		t.Fatalf("createFarSecretFromDockerConfig: %v", err)
	}
	// No panic/error is the assertion here — a real overwrite attempt would
	// still succeed against the fake, so also assert the original (empty)
	// Data survived untouched.
	sec, _ := k8sClient.CoreV1().Secrets("f5-cne-core").Get(context.Background(), farSecretName, metav1.GetOptions{})
	if len(sec.Data) != 0 {
		t.Error("expected the pre-existing far-secret to be left untouched")
	}
}

// TestCopyFarSecretFromContext_StripsMetadataAndCopiesData proves the copy
// path carries over Type+Data but not the source's server-set metadata,
// matching bash's bnk::copy_far_secret_from_context strip list (uid,
// resourceVersion, creationTimestamp, namespace, annotations, managedFields).
func TestCopyFarSecretFromContext_StripsMetadataAndCopiesData(t *testing.T) {
	srcClient := fakek8s.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        farSecretName,
			Namespace:   "f5-cne-core",
			UID:         "some-uid",
			Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "{}"},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{".dockerconfigjson": []byte("secret-bytes")},
	})
	dstClient := fakek8s.NewClientset()

	if err := copyFarSecretFromContext(context.Background(), srcClient, "f5-cne-core", dstClient, "f5-utils"); err != nil {
		t.Fatalf("copyFarSecretFromContext: %v", err)
	}

	got, err := dstClient.CoreV1().Secrets("f5-utils").Get(context.Background(), farSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting copied far-secret: %v", err)
	}
	if got.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("Type = %s, want %s", got.Type, corev1.SecretTypeDockerConfigJson)
	}
	if string(got.Data[".dockerconfigjson"]) != "secret-bytes" {
		t.Errorf("Data not copied: got %v", got.Data)
	}
	if got.UID == "some-uid" {
		t.Error("expected source UID to be stripped, not copied verbatim")
	}
	if got.Namespace != "f5-utils" {
		t.Errorf("Namespace = %s, want f5-utils (the destination, not the source)", got.Namespace)
	}
}

func TestCopyFarSecretFromContext_SkipsWhenDestinationHasSecret(t *testing.T) {
	srcClient := fakek8s.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: "f5-cne-core"},
		Data:       map[string][]byte{".dockerconfigjson": []byte("from-source")},
	})
	dstClient := fakek8s.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: "f5-utils"},
		Data:       map[string][]byte{".dockerconfigjson": []byte("pre-existing")},
	})

	if err := copyFarSecretFromContext(context.Background(), srcClient, "f5-cne-core", dstClient, "f5-utils"); err != nil {
		t.Fatalf("copyFarSecretFromContext: %v", err)
	}

	got, _ := dstClient.CoreV1().Secrets("f5-utils").Get(context.Background(), farSecretName, metav1.GetOptions{})
	if string(got.Data[".dockerconfigjson"]) != "pre-existing" {
		t.Error("expected the pre-existing destination far-secret to be left untouched")
	}
}

// writeTempFile writes content to a temp file and returns its path. Shared
// by the far-secret dockerconfig tests and the License JWT tests.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp file %s: %v", path, err)
	}
	return path
}

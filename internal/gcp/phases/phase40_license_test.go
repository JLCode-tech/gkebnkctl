package phases

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase40License_DryRun_NoClientsOrJwtNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	// JwtPath is intentionally left empty (testCluster()'s default) — the
	// dry-run branch must return before ever reading cl.Bnk.JwtPath.
	if err := Phase40License(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase40License(dryRun=true): %v", err)
	}
}

func TestPhase40License_MissingJwtFileErrors(t *testing.T) {
	cl := testCluster()
	cl.Bnk.JwtPath = filepath.Join(t.TempDir(), "does-not-exist.jwt")
	st := testState(t)
	err := Phase40License(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error for a missing JWT file")
	}
	if !strings.Contains(err.Error(), "reading JWT") {
		t.Errorf("error = %q, want a JWT-read error", err.Error())
	}
}

// TestPhase40License_LiveMode_RendersBeforeFailingOnNilClients proves the
// JWT is read + trimmed + rendered successfully before the (expected, since
// Clients.Dynamic is nil here) apply failure.
func TestPhase40License_LiveMode_RendersBeforeFailingOnNilClients(t *testing.T) {
	cl := testCluster()
	cl.Bnk.JwtPath = writeTempFile(t, "  eyFAKE.JWT.TOKEN  \n")
	st := testState(t)

	err := Phase40License(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if strings.Contains(err.Error(), "reading JWT") || strings.Contains(err.Error(), "rendering") {
		t.Errorf("expected JWT read + render to succeed and the apply step to fail, got: %v", err)
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

func TestPurgeStaleCPCLSecrets_SyncsKeyAndDeletesStaleSecrets(t *testing.T) {
	ctx := context.Background()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cpcl-key-cm", Namespace: "f5-utils"},
		Data:       map[string]string{"jwt.key": `{"keys":[{"kid":"v1-rotated"}]}`},
	}
	keySecUtils := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cpcl-key-secret", Namespace: "f5-utils"},
		Data:       map[string][]byte{"jwt.key": []byte("stale-key")},
	}
	keySecCore := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cpcl-key-secret", Namespace: "f5-cne-core"},
	}
	staleSecUtils := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "licensestatus", Namespace: "f5-utils"},
	}
	staleSecCore := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "f5-Flo-lic-msg", Namespace: "f5-cne-core"},
	}
	staleSecBnk := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cpcl-Dynamic-state", Namespace: "f5-bnk"},
	}

	fakeK8s := k8sfake.NewClientset(cm, keySecUtils, keySecCore, staleSecUtils, staleSecCore, staleSecBnk)
	clients := &gcp.Clients{K8s: fakeK8s}
	syncCPCLKeySecret(ctx, clients)
	purgeStaleCPCLSecrets(ctx, clients)

	for _, ns := range []string{"f5-utils", "f5-cne-core"} {
		got, err := fakeK8s.CoreV1().Secrets(ns).Get(ctx, "cpcl-key-secret", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get cpcl-key-secret in %s: %v", ns, err)
		}
		if string(got.Data["jwt.key"]) != `{"keys":[{"kid":"v1-rotated"}]}` {
			t.Errorf("cpcl-key-secret in %s = %q, want rotated JWKS", ns, string(got.Data["jwt.key"]))
		}
	}

	for _, tc := range []struct{ ns, name string }{
		{"f5-utils", "licensestatus"},
		{"f5-cne-core", "f5-Flo-lic-msg"},
		{"f5-bnk", "cpcl-Dynamic-state"},
	} {
		if _, err := fakeK8s.CoreV1().Secrets(tc.ns).Get(ctx, tc.name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Errorf("expected stale secret %s/%s to be deleted, err=%v", tc.ns, tc.name, err)
		}
	}
}

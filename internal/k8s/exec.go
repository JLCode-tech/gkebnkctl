package k8s

import (
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecCapture runs command inside ns/pod (scoped to container, if non-empty)
// via a non-interactive SPDY exec stream and returns its captured
// stdout/stderr. Added in Task D for Phase50's egress verification
// (docs/SPEC-go-tracer.md §3 Phase50: "verification via client-go exec").
//
// Unlike awsbnkctl's internal/k8s/exec.go ExecOptions.Run (which proxies
// live stdio for the interactive `k exec` CLI), this helper buffers output
// for programmatic use and needs no IOStreams/TTY plumbing.
//
// SPDY exec needs a real upgradeable HTTP connection to an API server, so it
// cannot be unit-tested directly — the same boundary awsbnkctl's
// exec_test.go documents ("SPDY exec needs a real upgradeable connection —
// not fakeable"). Callers that need test coverage over their exec-driven
// logic should depend on a function value that wraps ExecCapture (see
// internal/gcp/phases/phase50_egress.go's egressExecFunc), not on
// ExecCapture itself.
func ExecCapture(ctx context.Context, cfg *rest.Config, clientset kubernetes.Interface, ns, pod, container string, command []string) (stdout, stderr string, err error) {
	if cfg == nil {
		return "", "", fmt.Errorf("k8s: ExecCapture: rest.Config is nil")
	}
	if clientset == nil {
		return "", "", fmt.Errorf("k8s: ExecCapture: clientset is nil")
	}
	if pod == "" {
		return "", "", fmt.Errorf("k8s: ExecCapture: pod name required")
	}
	if len(command) == 0 {
		return "", "", fmt.Errorf("k8s: ExecCapture: command required")
	}

	req := clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod).
		Namespace(ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("k8s: ExecCapture: SPDY executor for %s/%s: %w", ns, pod, err)
	}

	var outBuf, errBuf bytes.Buffer
	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &outBuf,
		Stderr: &errBuf,
	})
	if streamErr != nil {
		return outBuf.String(), errBuf.String(), fmt.Errorf("k8s: ExecCapture: exec %v in %s/%s: %w", command, ns, pod, streamErr)
	}
	return outBuf.String(), errBuf.String(), nil
}

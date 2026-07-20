package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// pollInterval is the fixed poll interval for every Wait* helper below,
// matching docs/SPEC-go-tracer.md §5 ("wait.PollUntilContextTimeout, 5s
// interval").
const pollInterval = 5 * time.Second

// WaitForDeploymentReady polls apps/v1 Deployment in ns/name until
// Status.AvailableReplicas == Status.Replicas (both > 0). Returns an error if
// the deadline passes before the Deployment is ready.
func WaitForDeploymentReady(ctx context.Context, clientset kubernetes.Interface, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		d, err := clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			// Deployment may not exist yet — keep polling.
			return false, nil //nolint:nilerr
		}
		desired := d.Status.Replicas
		available := d.Status.AvailableReplicas
		return desired > 0 && available == desired, nil
	})
}

// WaitForDaemonSetReady polls apps/v1 DaemonSet in ns/name until
// Status.NumberReady == Status.DesiredNumberScheduled (both > 0).
func WaitForDaemonSetReady(ctx context.Context, clientset kubernetes.Interface, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		ds, err := clientset.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		desired := ds.Status.DesiredNumberScheduled
		ready := ds.Status.NumberReady
		return desired > 0 && ready == desired, nil
	})
}

// CertificateGVR is the GroupVersionResource for cert-manager Certificate CRs.
var CertificateGVR = schema.GroupVersionResource{
	Group:    "cert-manager.io",
	Version:  "v1",
	Resource: "certificates",
}

// WaitForCertificateReady polls cert-manager.io/v1 Certificate in ns/name via
// the dynamic client until the Ready condition is True.
func WaitForCertificateReady(ctx context.Context, dyn dynamic.Interface, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		obj, err := dyn.Resource(CertificateGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		return IsCertificateReady(obj.Object), nil
	})
}

// IsCertificateReady walks the unstructured status.conditions slice to find
// a condition with type=Ready and status=True.
func IsCertificateReady(obj map[string]interface{}) bool {
	status, ok := obj["status"].(map[string]interface{})
	if !ok {
		return false
	}
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t != "Ready" {
			continue
		}
		if s, _ := cond["status"].(string); s == "True" {
			return true
		}
	}
	return false
}

// CRDGVR is the GroupVersionResource for apiextensions.k8s.io/v1
// CustomResourceDefinition.
var CRDGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// WaitForCRDEstablished polls until the named CRD is visible + Established in
// the API server. Used to confirm the FLO/cert-manager CRDs are ready before
// applying CRs against them.
func WaitForCRDEstablished(ctx context.Context, dyn dynamic.Interface, crdName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		obj, err := dyn.Resource(CRDGVR).Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		return isCRDEstablished(obj.Object), nil
	})
}

// isCRDEstablished mirrors IsCertificateReady's condition walk, checking for
// type=Established status=True instead.
func isCRDEstablished(obj map[string]interface{}) bool {
	status, ok := obj["status"].(map[string]interface{})
	if !ok {
		return false
	}
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t != "Established" {
			continue
		}
		if s, _ := cond["status"].(string); s == "True" {
			return true
		}
	}
	return false
}

// WaitForPodReady polls a Pod in ns/name until all its containers report
// Ready (PodReady condition == True).
func WaitForPodReady(ctx context.Context, clientset kubernetes.Interface, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := clientset.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		if pod.Status.Phase == corev1.PodFailed {
			return false, fmt.Errorf("pod %s/%s entered Failed phase", ns, name)
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
}

// WaitForUnstructuredCondition polls a namespaced CR via the dynamic client
// until the string field at jsonPath (dot notation, e.g. "status.state")
// equals expectedValue. Only single-level nesting under status is supported
// (sufficient for the F5 BNK CRs). Returns an error containing the last-seen
// value if the timeout expires.
func WaitForUnstructuredCondition(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name, jsonPath, expectedValue string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fields := splitDotPath(jsonPath)
	var lastSeen string
	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		obj, err := dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		val, _, _ := unstructured.NestedString(obj.Object, fields...)
		lastSeen = val
		return val == expectedValue, nil
	})
	if err != nil {
		return fmt.Errorf("WaitForUnstructuredCondition: %s/%s %s: timeout waiting for %q, last seen %q: %w",
			namespace, name, jsonPath, expectedValue, lastSeen, err)
	}
	return nil
}

// splitDotPath splits a dot-separated jsonPath string into field segments.
// "status.state" -> ["status", "state"].
func splitDotPath(path string) []string {
	if path == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}
	return parts
}

// DeploymentReplicaStatus is a pure read used by postflight-style checks. It
// returns (available, desired, error).
func DeploymentReplicaStatus(ctx context.Context, clientset kubernetes.Interface, ns, name string) (available, desired int32, err error) {
	d, err := clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("get deployment %s/%s: %w", ns, name, err)
	}
	return d.Status.AvailableReplicas, d.Status.Replicas, nil
}

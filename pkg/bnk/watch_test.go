package bnk

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func makeEndpointSlice(ns, name, serviceName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "discovery.k8s.io/v1",
			"kind":       "EndpointSlice",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": ns,
				"labels": map[string]interface{}{
					serviceNameLabel: serviceName,
				},
			},
			"addressType": "IPv4",
		},
	}
}

func routePatchCount(dyn *dynamicfake.FakeDynamicClient) int {
	n := 0
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "patch" && a.GetResource() == httpRouteGVR {
			n++
		}
	}
	return n
}

func waitForPatches(dyn *dynamicfake.FakeDynamicClient, want int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n := routePatchCount(dyn); n >= want {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
	return routePatchCount(dyn)
}

func TestWatch_SliceChangeTriggersResync(t *testing.T) {
	route := makeHTTPRoute("f5-bnk", "nginx-route", 1)
	dyn := newFakeDynamic(route)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- WatchHTTPRoutes(ctx, dyn, WatchOptions{
			ResyncOptions: ResyncOptions{Namespace: "f5-bnk", Name: "nginx-route"},
			Debounce:      10 * time.Millisecond,
			IndexRefresh:  time.Hour,
		})
	}()

	time.Sleep(100 * time.Millisecond)

	if _, err := dyn.Resource(endpointSliceGVR).Namespace("f5-bnk").Create(
		ctx, makeEndpointSlice("f5-bnk", "my-svc-abc12", "my-svc"), metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("creating EndpointSlice: %v", err)
	}

	if n := waitForPatches(dyn, 2, 3*time.Second); n < 2 {
		t.Fatalf("EndpointSlice change did not trigger a resync (%d patch action(s) recorded)", n)
	}

	got, err := dyn.Resource(httpRouteGVR).Namespace("f5-bnk").Get(context.Background(), "nginx-route", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	rules, _, _ := unstructured.NestedSlice(got.Object, "spec", "rules")
	ref := rules[0].(map[string]interface{})["backendRefs"].([]interface{})[0].(map[string]interface{})
	if w, exists := ref["weight"]; !exists || w.(int64) != 1 {
		t.Errorf("expected weight 1, got %v", w)
	}
}

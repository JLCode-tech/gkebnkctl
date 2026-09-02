package test

import (
	"context"
	"testing"
	"time"
)

func TestCalcRTT(t *testing.T) {
	rtts := []float64{10.0, 20.0, 30.0, 40.0, 50.0}
	dist := calcRTT(rtts)

	if dist.Min != 10.0 {
		t.Errorf("expected Min 10.0, got %f", dist.Min)
	}
	if dist.Max != 50.0 {
		t.Errorf("expected Max 50.0, got %f", dist.Max)
	}
	if dist.P50 != 30.0 {
		t.Errorf("expected P50 30.0, got %f", dist.P50)
	}
}

func TestConnectivityLocal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cfg := ConnectivityConfig{
		Target:  "127.0.0.1:9999",
		Proto:   "tcp",
		Timeout: 100 * time.Millisecond,
		Count:   1,
	}

	res, err := RunConnectivity(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if res.Successful != 0 {
		t.Errorf("expected 0 successes for closed port, got %d", res.Successful)
	}
}

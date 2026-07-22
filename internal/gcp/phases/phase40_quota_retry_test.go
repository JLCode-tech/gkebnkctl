package phases

import (
	"context"
	"errors"
	"testing"
)

// The quota-status-unknown admission race is transient — the apply must be
// retried until the ResourceQuota controller catches up.
func TestRetryOnQuotaStatusUnknown_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	err := retryOnQuotaStatusUnknown(context.Background(), 0, func() error {
		calls++
		if calls < 3 {
			return errors.New(`licenses.k8s.f5net.com "bnk-license" is forbidden: status unknown for quota: f5-single-license-quota`)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected eventual success, got: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

// A different error (e.g. a genuine over-quota rejection) must NOT be retried —
// it returns immediately on the first attempt.
func TestRetryOnQuotaStatusUnknown_DoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	sentinel := errors.New("exceeded quota: f5-single-license-quota, used: 1, limited: 1")
	err := retryOnQuotaStatusUnknown(context.Background(), 0, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected the original error returned unretried, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("a non-quota-race error must not be retried, got %d calls", calls)
	}
}

// The retry is bounded — a persistent quota-status-unknown returns the error
// after the attempt cap rather than looping forever.
func TestRetryOnQuotaStatusUnknown_BoundedThenReturnsError(t *testing.T) {
	calls := 0
	err := retryOnQuotaStatusUnknown(context.Background(), 0, func() error {
		calls++
		return errors.New("status unknown for quota: f5-single-license-quota")
	})
	if err == nil {
		t.Fatal("expected an error after the retry cap")
	}
	if calls != licenseQuotaRetryAttempts {
		t.Errorf("expected exactly %d attempts, got %d", licenseQuotaRetryAttempts, calls)
	}
}

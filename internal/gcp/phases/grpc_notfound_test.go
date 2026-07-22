package phases

import (
	"testing"

	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The GKE container API is gRPC-backed: its errors arrive wrapped in
// apierror.APIError with HTTPCode() == -1 and the real code on the gRPC
// status. Checking HTTPCode() alone always reported false, which inverted
// Phase20's "does this cluster exist?" gate and made `up` unable to create a
// greenfield cluster. Live-hit 2026-07-21 on cluster bnk-scratch.
func TestIsNotFound_GRPCAPIError(t *testing.T) {
	grpcErr := status.Error(codes.NotFound,
		"Not found: projects/p/zones/us-central1-a/clusters/bnk-scratch.")
	aerr, ok := apierror.FromError(grpcErr)
	if !ok {
		t.Fatal("apierror.FromError did not wrap a grpc status error")
	}
	if aerr.HTTPCode() != -1 {
		t.Fatalf("precondition changed: HTTPCode() = %d, want -1", aerr.HTTPCode())
	}
	if !isNotFound(aerr) {
		t.Error("isNotFound(gRPC NotFound APIError) = false, want true — Phase20 cannot create a cluster")
	}
}

func TestIsPermissionDenied_GRPCAPIError(t *testing.T) {
	aerr, ok := apierror.FromError(status.Error(codes.PermissionDenied, "denied"))
	if !ok {
		t.Fatal("apierror.FromError did not wrap a grpc status error")
	}
	if !isPermissionDenied(aerr) {
		t.Error("isPermissionDenied(gRPC PermissionDenied APIError) = false, want true")
	}
}

// An unrelated gRPC code must not be mistaken for NotFound/PermissionDenied,
// even though the old substring fallback could have matched on message text.
func TestGRPCAPIError_UnrelatedCodeIsNotMatched(t *testing.T) {
	aerr, ok := apierror.FromError(status.Error(codes.Unavailable, "backend not found right now"))
	if !ok {
		t.Fatal("apierror.FromError did not wrap a grpc status error")
	}
	if isNotFound(aerr) {
		t.Error("isNotFound(gRPC Unavailable) = true, want false")
	}
	if isPermissionDenied(aerr) {
		t.Error("isPermissionDenied(gRPC Unavailable) = true, want false")
	}
}

// Raw grpc status errors (not wrapped by apierror) must still work via the
// substring fallback the fakes rely on.
func TestIsNotFound_RawGRPCStatusError(t *testing.T) {
	if !isNotFound(status.Error(codes.NotFound, "Not found: clusters/x")) {
		t.Error("isNotFound(raw grpc NotFound) = false, want true")
	}
}

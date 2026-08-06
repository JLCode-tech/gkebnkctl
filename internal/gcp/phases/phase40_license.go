package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

// licenseCRTemplatePath is the embedded template Phase40License renders.
const licenseCRTemplatePath = "templates/license-cr.yaml.tmpl"

// Phase40License reads the JWT at cl.Bnk.JwtPath, trims whitespace, renders
// the License/bnk-license CR (connected mode, raw un-encoded JWT — live-
// proven, do NOT base64-encode it) entirely in memory, and applies it via
// dynamic SSA. The JWT content is never written to disk — it is read at
// phase entry and inlined directly into the render call
// (docs/SPEC-go-tracer.md §2 "Secrets constraint").
func Phase40License(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 40] License: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 40] would apply License/bnk-license in f5-utils (connected mode, JWT never written to disk)")
		return nil
	}

	jwtRaw, err := os.ReadFile(cl.Bnk.JwtPath) //nolint:gosec -- path is operator-supplied via cluster.yaml, existence-validated at intent.Load
	if err != nil {
		return fmt.Errorf("phase40: reading JWT %s: %w", cl.Bnk.JwtPath, err)
	}
	jwt := strings.TrimSpace(string(jwtRaw))

	tmplBytes, err := manifests.FS.ReadFile(licenseCRTemplatePath)
	if err != nil {
		return fmt.Errorf("phase40: reading embedded license-cr template: %w", err)
	}
	rendered, err := render.RenderLicenseCR(tmplBytes, render.LicenseCRVars{JWT: jwt})
	if err != nil {
		return fmt.Errorf("phase40: rendering license-cr: %w", err)
	}

	if err := applyLicenseWithQuotaRetry(ctx, clients, rendered); err != nil {
		return fmt.Errorf("phase40: applying License CR: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 40] License OK")
	return nil
}

const (
	// licenseQuotaRetryAttempts / Interval bound the retry for the transient
	// ResourceQuota admission race below.
	licenseQuotaRetryAttempts = 12
	licenseQuotaRetryInterval = 5 * time.Second

	// quotaStatusUnknownMarker is the substring the API server returns when a
	// ResourceQuota exists but its controller has not yet populated
	// .status.used, so admission cannot decide and rejects the request:
	//   licenses.k8s.f5net.com "bnk-license" is forbidden: status unknown for
	//   quota: f5-single-license-quota, resources: count/licenses.k8s.f5net.com
	// FLO creates f5-single-license-quota moments before Phase40 applies the
	// License, so on a greenfield run this fires until the quota controller
	// catches up — a few seconds (live-hit 2026-07-21, cluster bnk-scratch3).
	// A re-run masked it because the quota status was populated by then.
	quotaStatusUnknownMarker = "status unknown for quota"
)

// applyLicenseWithQuotaRetry applies the License CR, retrying only the
// transient "status unknown for quota" admission rejection. Any other error is
// returned immediately — notably a genuine over-quota rejection (which would
// not carry this marker) is not retried.
func applyLicenseWithQuotaRetry(ctx context.Context, clients *gcp.Clients, rendered []byte) error {
	return retryOnQuotaStatusUnknown(ctx, licenseQuotaRetryInterval, func() error {
		return applyManifestBytes(ctx, clients, rendered, false)
	})
}

// retryOnQuotaStatusUnknown runs apply, retrying ONLY the transient
// "status unknown for quota" admission rejection up to licenseQuotaRetryAttempts
// times. apply is injectable so the retry decision is unit-tested without a
// live cluster.
func retryOnQuotaStatusUnknown(ctx context.Context, interval time.Duration, apply func() error) error {
	attempt := 0
	var lastErr error
	err := gcp.PollUntil(ctx, interval, time.Duration(licenseQuotaRetryAttempts)*interval+time.Second, func(ctx context.Context) (bool, error) {
		attempt++
		lastErr = apply()
		if lastErr == nil {
			return true, nil
		}
		if !strings.Contains(lastErr.Error(), quotaStatusUnknownMarker) {
			return false, lastErr
		}
		if attempt >= licenseQuotaRetryAttempts {
			return false, lastErr
		}
		fmt.Fprintf(os.Stderr, "[phase 40] License rejected while the ResourceQuota controller catches up (attempt %d/%d) — retrying in %s\n",
			attempt, licenseQuotaRetryAttempts, interval)
		return false, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return lastErr
		}
		return err
	}
	return nil
}

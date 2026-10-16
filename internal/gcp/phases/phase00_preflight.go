// Package phases implements the gkebnkctl provisioning graph (Phase00..
// Phase50 + down counterparts). Every phase follows the signature
// func PhaseNN(ctx, cl *intent.Cluster, st *state.State, clients *gcp.Clients,
// dryRun bool) error — see docs/SPEC-go-tracer.md §3.
package phases

import (
	"context"
	"fmt"
	"os"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"golang.org/x/oauth2/google"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/jwt"
)

// cloudPlatformScope is the OAuth2 scope requested when verifying
// Application Default Credentials are present.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// requiredAPIs are the two GCP APIs every gkebnkctl cluster needs enabled.
var requiredAPIs = []string{"compute.googleapis.com", "container.googleapis.com"}

// regionQuotaFloor is the minimum acceptable (limit - usage) headroom for the
// CPUS / IN_USE_ADDRESSES region quotas before Phase00 prints a warning. This
// is a best-effort heads-up, not a hard gate — see docs/SPEC-go-tracer.md §3.
const regionQuotaFloor = 8

// Phase00Preflight runs before any provisioning: verifies Application
// Default Credentials are present, verifies compute.googleapis.com and
// container.googleapis.com are enabled on the target project, and prints a
// best-effort region-quota warning (CPUS / IN_USE_ADDRESSES headroom).
//
// Read-only and fail-fast — Phase00 creates nothing. Runs identically in
// dry-run and live mode (it is the only phase permitted to touch auth/APIs
// during --dry-run, per docs/SPEC-go-tracer.md §6), UNLESS
// GKEBNKCTL_SKIP_AUTH=1 is set, in which case runPhasedUp/runPhasedDown
// (internal/cli/lifecycle.go) pass a nil *gcp.Clients and this phase skips
// every real check — the CI/test hook for running --dry-run without live
// GCP credentials.
func Phase00Preflight(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, _ bool) error {
	fmt.Fprintf(os.Stderr, "[phase 00] preflight: cluster=%s project=%s zone=%s\n",
		cl.Metadata.Name, cl.Metadata.Project, cl.Metadata.Zone)

	if cl.Bnk.JwtPath != "" {
		// #nosec G304 -- JwtPath is user-configured license path from cluster.yaml
		if data, err := os.ReadFile(cl.Bnk.JwtPath); err == nil {
			if _, chkErr := jwt.CheckPreflight(string(data), os.Stderr); chkErr != nil {
				fmt.Fprintf(os.Stderr, "[phase 00] note: JWT parsing failed (%v) - skipping claims inspection\n", chkErr)
			}
		}
	}

	if clients == nil {
		fmt.Fprintln(os.Stderr, "[phase 00] GKEBNKCTL_SKIP_AUTH=1: skipping ADC + API-enabled checks")
		return nil
	}

	if _, err := google.FindDefaultCredentials(ctx, cloudPlatformScope); err != nil {
		return fmt.Errorf("phase00: application default credentials: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 00] ADC ok (project %s)\n", cl.Metadata.Project)

	for _, api := range requiredAPIs {
		enabled, err := checkAPIEnabled(ctx, clients.ServiceUsage, cl.Metadata.Project, api)
		if err != nil {
			return fmt.Errorf("phase00: checking %s enabled: %w", api, err)
		}
		if !enabled {
			return fmt.Errorf(
				"phase00: required API %s is not enabled on project %s (enable it: gcloud services enable %s --project=%s)",
				api, cl.Metadata.Project, api, cl.Metadata.Project,
			)
		}
	}
	fmt.Fprintf(os.Stderr, "[phase 00] APIs enabled: %v\n", requiredAPIs)

	warnRegionQuota(ctx, clients.Regions, cl.Metadata.Project, cl.Region)

	fmt.Fprintln(os.Stderr, "[phase 00] preflight OK")
	return nil
}

// checkAPIEnabled reports whether api is enabled for project via the
// serviceusage v1 GetService call.
func checkAPIEnabled(ctx context.Context, su gcp.ServiceUsageAPI, project, api string) (bool, error) {
	name := fmt.Sprintf("projects/%s/services/%s", project, api)
	svc, err := su.GetService(ctx, name)
	if err != nil {
		return false, err
	}
	return svc.State == "ENABLED", nil
}

// warnRegionQuota is a best-effort, warn-only check of the CPUS and
// IN_USE_ADDRESSES region quotas. Any error (including a nil Regions client)
// is swallowed with a warning — it must never fail preflight.
func warnRegionQuota(ctx context.Context, regions gcp.RegionsAPI, project, region string) {
	if regions == nil || region == "" {
		return
	}
	out, err := regions.Get(ctx, &computepb.GetRegionRequest{Project: project, Region: region})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[phase 00] warn: region quota check skipped: %v\n", err)
		return
	}
	for _, q := range out.GetQuotas() {
		metric := q.GetMetric()
		if metric != "CPUS" && metric != "IN_USE_ADDRESSES" {
			continue
		}
		headroom := q.GetLimit() - q.GetUsage()
		if headroom < regionQuotaFloor {
			fmt.Fprintf(os.Stderr, "[phase 00] warn: region %s metric %s low headroom (usage=%.0f limit=%.0f)\n",
				region, metric, q.GetUsage(), q.GetLimit())
		}
	}
}

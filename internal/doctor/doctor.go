package doctor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"golang.org/x/oauth2/google"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

// CheckStatus represents the outcome of a doctor diagnostic check.
type CheckStatus string

const (
	StatusOK      CheckStatus = "ok"
	StatusWarning CheckStatus = "warn"
	StatusError   CheckStatus = "fail"
	StatusSkipped CheckStatus = "skip"
)

// Check is a single diagnostic observation.
type Check struct {
	Name     string      `json:"name"`
	Status   CheckStatus `json:"status"`
	Detail   string      `json:"detail"`
	Why      string      `json:"why,omitempty"`
	Hint     string      `json:"hint,omitempty"`
	Optional bool        `json:"optional,omitempty"`
}

// Config holds options for running doctor.
type Config struct {
	Project string
	Region  string
	Strict  bool
}

// cloudPlatformScope is the OAuth2 scope requested for ADC verification.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// requiredAPIs are the core GCP APIs gkebnkctl interacts with.
var requiredAPIs = []struct {
	API string
	Why string
}{
	{"compute.googleapis.com", "VPCs, subnets, firewalls, Cloud NAT, and routes"},
	{"container.googleapis.com", "GKE cluster and node pool provisioning"},
	{"iam.googleapis.com", "Service account and IAM permissions"},
}

// Run executes all diagnostic checks and returns the results slice.
func Run(ctx context.Context, cfg Config) []Check {
	var results []Check

	// 1. Tooling checks
	results = append(results, checkBinary(ctx, "gcloud", false, "Google Cloud CLI for authentication and SDK helpers"))
	results = append(results, checkBinary(ctx, "kubectl", true, "Kubernetes CLI (optional; gkebnkctl has client-go in-process)"))

	// 2. ADC Authentication
	adcCheck, creds := checkADC(ctx)
	results = append(results, adcCheck)

	project := cfg.Project
	if project == "" && creds != nil && creds.ProjectID != "" {
		project = creds.ProjectID
	}

	// 3. Project & APIs Check
	if project == "" {
		results = append(results, Check{
			Name:     "gcp project",
			Status:   StatusWarning,
			Detail:   "no project specified (set --project or configure via gcloud config set project <id>)",
			Why:      "target GCP project for infrastructure",
			Optional: true,
		})
	} else {
		results = append(results, Check{
			Name:   "gcp project",
			Status: StatusOK,
			Detail: project,
			Why:    "target GCP project for infrastructure",
		})

		// API Service Usage Checks
		apiChecks := checkAPIs(ctx, project)
		results = append(results, apiChecks...)

		// Quotas Check
		if cfg.Region != "" {
			results = append(results, checkQuotas(ctx, project, cfg.Region))
		}
	}

	return results
}

func checkBinary(ctx context.Context, name string, optional bool, why string) Check {
	path, err := exec.LookPath(name)
	if err != nil {
		status := StatusError
		if optional {
			status = StatusOK
			return Check{
				Name:     name,
				Status:   status,
				Detail:   "not on PATH (optional; client-go internalized)",
				Why:      why,
				Optional: optional,
			}
		}
		return Check{
			Name:     name,
			Status:   status,
			Detail:   "not found on PATH",
			Why:      why,
			Hint:     installHint(name),
			Optional: optional,
		}
	}

	ver := versionLine(ctx, name)
	detail := path
	if ver != "" {
		detail = fmt.Sprintf("%s (%s)", path, ver)
	}

	return Check{
		Name:     name,
		Status:   StatusOK,
		Detail:   detail,
		Why:      why,
		Optional: optional,
	}
}

func versionLine(ctx context.Context, name string) string {
	var args []string
	switch name {
	case "gcloud":
		args = []string{"version"}
	case "kubectl":
		args = []string{"version", "--client=true", "--output=yaml"}
	default:
		args = []string{"--version"}
	}

	vctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(vctx, name, args...)
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return ""
	}

	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func checkADC(ctx context.Context) (Check, *google.Credentials) {
	const why = "verifies GCP Application Default Credentials are valid"
	creds, err := google.FindDefaultCredentials(ctx, cloudPlatformScope)
	if err != nil {
		return Check{
			Name:   "gcp adc",
			Status: StatusError,
			Detail: "no valid credentials found: " + err.Error(),
			Why:    why,
			Hint:   "       → run: gcloud auth application-default login\n",
		}, nil
	}

	detail := "valid"
	if creds.ProjectID != "" {
		detail = fmt.Sprintf("valid (project: %s)", creds.ProjectID)
	}
	return Check{
		Name:   "gcp adc",
		Status: StatusOK,
		Detail: detail,
		Why:    why,
	}, creds
}

func checkAPIs(ctx context.Context, project string) []Check {
	clients, err := gcp.NewClients(ctx, project)
	if err != nil {
		return []Check{{
			Name:   "gcp apis",
			Status: StatusWarning,
			Detail: "could not initialize GCP client: " + err.Error(),
			Why:    "requires GCP API connectivity",
		}}
	}

	var checks []Check
	for _, req := range requiredAPIs {
		name := fmt.Sprintf("projects/%s/services/%s", project, req.API)
		svc, err := clients.ServiceUsage.GetService(ctx, name)
		if err != nil {
			checks = append(checks, Check{
				Name:   req.API,
				Status: StatusWarning,
				Detail: "unable to query status: " + err.Error(),
				Why:    req.Why,
			})
			continue
		}

		if svc.State == "ENABLED" {
			checks = append(checks, Check{
				Name:   req.API,
				Status: StatusOK,
				Detail: "enabled",
				Why:    req.Why,
			})
		} else {
			checks = append(checks, Check{
				Name:   req.API,
				Status: StatusError,
				Detail: "disabled",
				Why:    req.Why,
				Hint:   fmt.Sprintf("       → run: gcloud services enable %s --project=%s\n", req.API, project),
			})
		}
	}
	return checks
}

func checkQuotas(ctx context.Context, project, region string) Check {
	const why = "warns if regional CPU or IP headroom is tight"
	clients, err := gcp.NewClients(ctx, project)
	if err != nil {
		return Check{
			Name:     "region quota",
			Status:   StatusSkipped,
			Detail:   "skipped (no client)",
			Why:      why,
			Optional: true,
		}
	}

	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := clients.Regions.Get(tctx, &computepb.GetRegionRequest{Project: project, Region: region})
	if err != nil {
		return Check{
			Name:     "region quota",
			Status:   StatusSkipped,
			Detail:   "skipped (" + err.Error() + ")",
			Why:      why,
			Optional: true,
		}
	}

	var notes []string
	atLimit := false
	for _, q := range out.GetQuotas() {
		metric := q.GetMetric()
		if metric != "CPUS" && metric != "IN_USE_ADDRESSES" {
			continue
		}
		notes = append(notes, fmt.Sprintf("%s: %.0f/%.0f", metric, q.GetUsage(), q.GetLimit()))
		if q.GetLimit()-q.GetUsage() < 8 {
			atLimit = true
		}
	}

	if len(notes) == 0 {
		return Check{
			Name:     "region quota",
			Status:   StatusOK,
			Detail:   fmt.Sprintf("region %s checked", region),
			Why:      why,
			Optional: true,
		}
	}

	detail := strings.Join(notes, ", ")
	status := StatusOK
	if atLimit {
		status = StatusWarning
		detail += " (low headroom; consider requesting a quota increase)"
	}

	return Check{
		Name:     "region quota",
		Status:   status,
		Detail:   fmt.Sprintf("[%s] %s", region, detail),
		Why:      why,
		Optional: true,
	}
}

func installHint(name string) string {
	goos := runtime.GOOS
	mac := goos == "darwin"
	switch name {
	case "gcloud":
		if mac {
			return "       → brew install --cask google-cloud-sdk\n" +
				"         docs: https://cloud.google.com/sdk/docs/install\n"
		}
		return "       → curl https://sdk.cloud.google.com | bash\n" +
			"         docs: https://cloud.google.com/sdk/docs/install\n"
	case "kubectl":
		if mac {
			return "       → brew install kubectl\n"
		}
		return "       → curl -LO \"https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl\" && chmod +x kubectl && sudo mv kubectl /usr/local/bin/\n"
	}
	return ""
}

// PrintResults renders the check table to w.
func PrintResults(w io.Writer, results []Check) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range results {
		sym := symbolFor(r.Status)
		why := ""
		if r.Why != "" {
			why = "(" + r.Why + ")"
		}
		fmt.Fprintf(tw, "  [%s] %-28s  %-35s  %s\n", sym, r.Name, r.Detail, why)
		if r.Hint != "" {
			fmt.Fprint(tw, r.Hint)
		}
	}
	return tw.Flush()
}

func symbolFor(s CheckStatus) string {
	switch s {
	case StatusOK:
		return "✓"
	case StatusWarning, StatusSkipped:
		return "!"
	default:
		return "✗"
	}
}

// HasFailures reports if any non-optional check failed.
func HasFailures(results []Check) bool {
	for _, r := range results {
		if r.Status == StatusError && !r.Optional {
			return true
		}
	}
	return false
}

// HasWarnings reports if any check raised a warning.
func HasWarnings(results []Check) bool {
	for _, r := range results {
		if r.Status == StatusWarning {
			return true
		}
	}
	return false
}

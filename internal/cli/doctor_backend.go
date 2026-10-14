package cli

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/JLCode-tech/gkebnkctl/internal/doctor"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/phases"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/bnkscan"
	"github.com/JLCode-tech/gkebnkctl/internal/test"
)

// runBackendChecks dispatches to the per-backend doctor probes. `spec` is one of:
//
//	k8s              -> cluster reachable, BNK readiness, the bnk heal detections
//
// Each probe returns one or more doctor.Check entries with BackendName set.
func runBackendChecks(ctx context.Context, spec string) []doctor.Check {
	switch {
	case spec == "k8s":
		return runK8sBackendChecks(ctx)
	default:
		return []doctor.Check{{
			Name:     "doctor backend " + spec,
			Status:   doctor.StatusError,
			Detail:   fmt.Sprintf("unsupported --backend value %q (want k8s)", spec),
			Optional: false,
		}}
	}
}

// runK8sBackendChecks probes the k8s execution backend's prerequisites:
//   - apiserver reachable (clientset construction succeeds)
//   - BNK API generation, Infra Programmed, Gateway Accepted+Programmed,
//     f5-cne-controller rollout (bnkscan), with a warning and
//     `gkebnkctl bnk migrate-2.4` hint when 2.3 CRDs or CRs remain
//   - every bnk heal repair Detect as a row (phases.DetectAll)
func runK8sBackendChecks(ctx context.Context) []doctor.Check {
	out := []doctor.Check{}
	add := func(name string, status doctor.CheckStatus, detail string) {
		out = append(out, doctor.Check{
			Name:        name,
			Status:      status,
			Detail:      detail,
			BackendName: "k8s",
		})
	}

	cs, err := k8s.BuildClientset("")
	if err != nil {
		add("k8s cluster reachable", doctor.StatusError, err.Error())
		return out
	}
	restCfg, restErr := k8s.BuildRESTConfig("")
	if restErr != nil {
		restCfg = nil
	}
	add("k8s cluster reachable", doctor.StatusOK, "kubeconfig loaded")

	// BNK readiness from the shared bnkscan index: API generation, Infra,
	// Gateways, controller, and a migrate-2.4 hint when 2.3 objects remain.
	idx, scanErr := ScanBNK(ctx, "", bnkscan.DefaultControllerNamespace, StatusScanTimeout)
	out = append(out, BnkDoctorChecks(idx, scanErr)...)
	healClients := &phases.Clients{K8s: cs}
	if restCfg != nil {
		if dyn, derr := dynamic.NewForConfig(restCfg); derr == nil {
			healClients.Dynamic = dyn
		}
	}
	out = append(out, HealDoctorChecks(ctx, healClients)...)

	return out
}

// RunDNSProbeCheck runs the embedded DNS probe against the target.
func RunDNSProbeCheck(ctx context.Context, target string) (doctor.Check, bool) {
	if target == "" {
		return doctor.Check{}, false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cfg := test.DNSProbeConfig{
		Target:     target,
		Type:       "A",
		Server:     "system",
		Iterations: 1,
		Timeout:    2 * time.Second,
	}
	res, err := test.RunDNS(probeCtx, cfg)
	c := doctor.Check{Name: "dns probe (" + target + ")"}
	if err != nil {
		c.Status = doctor.StatusError
		c.Detail = err.Error()
		return c, true
	}
	if res.Error != "" {
		c.Status = doctor.StatusError
		c.Detail = fmt.Sprintf("%s: %s", res.Rcode, res.Error)
		return c, true
	}
	if len(res.Answers) == 0 {
		c.Status = doctor.StatusWarning
		c.Detail = fmt.Sprintf("no answers (rcode=%s, server=%s)", res.Rcode, res.Server)
		return c, true
	}
	c.Status = doctor.StatusOK
	c.Detail = fmt.Sprintf("%d answer(s) in %.1fms (server=%s)", len(res.Answers), res.LatencyMs.P50, res.Server)
	return c, true
}

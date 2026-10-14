package cli

// bnk_readiness.go -- the one place the CLI reads a cluster's BNK state.
// status, doctor, logs --governance, targets scan, benchmark setup
// --auto-discover and forge scan all go through these helpers so they share
// bnkscan's index, readiness verdict and MCP discovery.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/doctor"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/bnkscan"
)

// scanClients builds the dynamic + typed clients bnkscan needs. Tests replace it.
var scanClients = func(kubeconfigPath string) (dynamic.Interface, kubernetes.Interface, error) {
	cfg, err := k8s.BuildRESTConfig(kubeconfigPath)
	if err != nil {
		return nil, nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("dynamic client: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("clientset: %w", err)
	}
	return dyn, cs, nil
}

// statusScanTimeout bounds the best-effort BNK scan in status and doctor.
const statusScanTimeout = 20 * time.Second

// scanBNK indexes the cluster behind kubeconfigPath within timeout.
func scanBNK(ctx context.Context, kubeconfigPath, controllerNS string, timeout time.Duration) (*bnkscan.Index, error) {
	dyn, cs, err := scanClients(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return bnkscan.Scan(sctx, dyn, cs, bnkscan.Options{ControllerNamespace: controllerNS})
}

// writeStatusBNK prints the BNK block of status. Best-effort like
// every other status section: a scan error is one line, not a failure.
func writeStatusBNK(w io.Writer, idx *bnkscan.Index, scanErr error) {
	if scanErr != nil {
		fmt.Fprintf(w, "BNK API:\t(scan failed: %v)\n", scanErr)
		return
	}
	rd := idx.Readiness()
	gen := string(rd.Generation)
	if len(idx.Groups) > 0 {
		gen += " (CRD groups: " + strings.Join(idx.Groups, ", ") + ")"
	}
	fmt.Fprintf(w, "BNK API:\t%s\n", gen)
	if rd.Generation == bnkscan.GenNone {
		fmt.Fprintln(w, "BNK readiness:\tnot installed")
		return
	}
	if rd.Generation == bnkscan.Gen24 || rd.Generation == bnkscan.GenMixed {
		fmt.Fprintf(w, "Infra:\t%s Programmed\n", rd.Infras)
	}
	fmt.Fprintf(w, "Gateways:\t%s Accepted+Programmed\n", rd.Gateways)
	if rd.Controller.Found {
		fmt.Fprintf(w, "CNE controller:\t%s/%s %d/%d available\n", rd.Controller.Namespace, rd.Controller.Name, rd.Controller.Available, rd.Controller.Desired)
	} else {
		fmt.Fprintf(w, "CNE controller:\t%s/%s not found\n", rd.Controller.Namespace, rd.Controller.Name)
	}
	if len(idx.MCP) > 0 {
		fmt.Fprintf(w, "MCP endpoints:\t%d\n", len(idx.MCP))
	}
	if rd.Ready {
		fmt.Fprintln(w, "BNK readiness:\tready")
	} else {
		fmt.Fprintf(w, "BNK readiness:\tnot ready -- %s\n", strings.Join(rd.Problems, "; "))
	}
	if advice := rd.MigrationAdvice(); advice != "" {
		fmt.Fprintf(w, "BNK migration:\t%s\n", advice)
	}
}

// bnkDoctorChecks renders the readiness verdict as doctor rows. Readiness
// failures are errors; a 2.3 or mixed API generation and leftover 2.3 objects
// are warnings that point at bnk upgrade / bnk migrate-2.4.
func bnkDoctorChecks(idx *bnkscan.Index, scanErr error) []doctor.Check {
	row := func(name string, status doctor.CheckStatus, detail string) doctor.Check {
		return doctor.Check{Name: name, Status: status, Detail: detail, BackendName: "k8s"}
	}
	if scanErr != nil {
		return []doctor.Check{row("bnk api generation", doctor.StatusWarning, "scan failed: "+scanErr.Error())}
	}
	rd := idx.Readiness()
	var out []doctor.Check
	groups := strings.Join(idx.Groups, ", ")
	switch rd.Generation {
	case bnkscan.Gen24:
		out = append(out, row("bnk api generation", doctor.StatusOK, "2.4 ("+groups+")"))
	case bnkscan.GenMixed:
		out = append(out, row("bnk api generation", doctor.StatusWarning, "mixed: "+rd.MigrationAdvice()))
	case bnkscan.Gen23:
		out = append(out, row("bnk api generation", doctor.StatusWarning, "2.3 ("+bnkscan.LegacyPolicyGroup+"); run `gkebnkctl bnk upgrade`"))
	default:
		out = append(out, row("bnk api generation", doctor.StatusWarning, "none -- BNK CRDs not served (not installed, or `gkebnkctl up` has not reached phase 22)"))
		return out
	}
	if rd.Generation == bnkscan.Gen24 || rd.Generation == bnkscan.GenMixed {
		st, detail := doctor.StatusOK, rd.Infras.String()+" Programmed=True"
		if rd.Infras.Total == 0 || rd.Infras.Ready < rd.Infras.Total {
			st = doctor.StatusError
			detail += " -- " + strings.Join(filterPrefix(rd.Problems, "Infra", "no Infra"), "; ")
		}
		out = append(out, row("bnk infra", st, detail))
		if rd.Generation == bnkscan.Gen24 && (rd.LegacyCRDs || rd.LegacyCRs > 0) {
			out = append(out, row("bnk legacy 2.3 objects", doctor.StatusWarning, rd.MigrationAdvice()))
		}
	}
	gwStatus, gwDetail := doctor.StatusOK, rd.Gateways.String()+" Accepted=True,Programmed=True"
	if rd.Gateways.Ready < rd.Gateways.Total {
		gwStatus = doctor.StatusError
		gwDetail += " -- " + strings.Join(filterPrefix(rd.Problems, "Gateway"), "; ")
	}
	out = append(out, row("bnk gateways", gwStatus, gwDetail))
	ctrl := rd.Controller
	switch {
	case !ctrl.Found:
		out = append(out, row("bnk cne controller", doctor.StatusError, fmt.Sprintf("deployment %s/%s not found", ctrl.Namespace, ctrl.Name)))
	case !ctrl.Ready:
		out = append(out, row("bnk cne controller", doctor.StatusError, fmt.Sprintf("%s/%s %d/%d replicas available", ctrl.Namespace, ctrl.Name, ctrl.Available, ctrl.Desired)))
	default:
		out = append(out, row("bnk cne controller", doctor.StatusOK, fmt.Sprintf("%s/%s %d/%d available", ctrl.Namespace, ctrl.Name, ctrl.Available, ctrl.Desired)))
	}
	return out
}

// filterPrefix keeps the lines starting with one of the prefixes.
func filterPrefix(lines []string, prefixes ...string) []string {
	var out []string
	for _, l := range lines {
		for _, p := range prefixes {
			if strings.HasPrefix(l, p) {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// warnf prints an operator warning to stderr without emojis.
func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[warn] "+format+"\n", args...)
}

// resolveKubeconfigFlags applies the shared precedence: explicit path, then
// the cluster.yaml state, then the kubectl default (empty string).
func resolveKubeconfigFlags(explicit, configPath string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if configPath == "" {
		return "", nil
	}
	return resolveKubeconfigFromConfig(configPath)
}

// resolveKubeconfigFromConfig derives a kubeconfig path from a cluster.yaml
// path by loading the cluster's state.env and reading KUBECONFIG_PATH.
func resolveKubeconfigFromConfig(configPath string) (string, error) {
	if configPath == "" {
		return "", nil
	}
	cl, err := intent.Load(configPath)
	if err != nil {
		return "", fmt.Errorf("loading --config %s: %w", configPath, err)
	}
	st, err := state.Load(cl.StateDir())
	if err != nil {
		return "", nil
	}
	stored := st.Get("KUBECONFIG_PATH")
	if stored == "" {
		stored = st.KubeconfigPath()
	}
	if stored != "" {
		if _, err := os.Stat(stored); err == nil {
			return stored, nil
		}
		candidate := filepath.Join(cl.StateDir(), filepath.Base(stored))
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if _, err := os.Stat("artifacts/kubeconfig"); err == nil {
		return "artifacts/kubeconfig", nil
	}
	return "", nil
}

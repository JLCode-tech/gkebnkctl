package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/scenarios"
)

var (
	kApplyFilename  string
	kApplyNamespace string
	kApplyForce     bool
	kApplyConfig    string
)

func newKApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply -f <file-or-dir> [-n <ns>] [--force]",
		Short: "Server-side apply YAML/JSON manifests, directories, or kustomize bases",
		Long: `Server-side apply with field-manager '` + k8s.FieldManager + `'.

  -f <file>       single YAML/JSON file (multi-doc YAML supported)
  -f <dir>        directory: kustomization.yaml-detected → krusty build;
                  otherwise recursive *.yaml / *.yml
  -f -            stdin (multi-doc YAML)

--force passes through to SSA's force-conflicts flag, identical to
kubectl apply --server-side --force-conflicts.

--config <cluster.yaml> renders the file as a Go template first, with every
state.env key ({{.VNetCIDR}}, {{.NODE_PRIMARY_IFNAME}}, …) plus the derived
network values {{.NodeIfname}} {{.ExtGateway}} {{.IntGateway}} {{.TMMExtSelfIP}}
{{.TMMIntSelfIP}}. This is how the files in examples/ stay free of hardcoded
interface names and addresses.

Examples:

  gkebnkctl k apply -f deploy.yaml -n f5-bnk
  gkebnkctl k apply -f manifests/
  gkebnkctl k apply --config examples/egress-demo/cluster.yaml -f examples/egress-demo/egress-toggle.yaml
  cat deploy.yaml | gkebnkctl k apply -f -`,
		RunE: runKApply,
	}
	flags := cmd.Flags()
	flags.StringVarP(&kApplyFilename, "filename", "f", "", "file, directory, or '-' for stdin")
	flags.StringVarP(&kApplyNamespace, "namespace", "n", "", "namespace for namespaced resources without an explicit namespace field")
	flags.BoolVar(&kApplyForce, "force", false, "force-conflicts on server-side apply (kubectl apply --force-conflicts)")
	flags.StringVar(&kApplyConfig, "config", "", "cluster.yaml whose state renders {{.…}} template values in the file (single file only)")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

// renderManifest executes the Go-template directives in content with the
// cluster's template vars; content without directives is returned unchanged.
func renderManifest(content string, cl *intent.Cluster, st *state.State) (string, error) {
	if !strings.Contains(content, "{{") {
		return content, nil
	}
	return scenarios.RenderTemplate(content, scenarios.TemplateVars(cl, st))
}

func init() {
	kCmd.AddCommand(newKApplyCmd())
}

func runKApply(cmd *cobra.Command, _ []string) error {
	opts := &k8s.ApplyOptions{
		Filename:  kApplyFilename,
		Namespace: kApplyNamespace,
		Force:     kApplyForce,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
	if kApplyConfig != "" {
		cl, err := intent.Load(kApplyConfig)
		if err != nil {
			return fmt.Errorf("k apply: %w", err)
		}
		st, err := state.Load(cl.StateDir())
		if err != nil {
			return fmt.Errorf("k apply: loading state: %w", err)
		}
		raw, err := os.ReadFile(kApplyFilename) // #nosec G304 -- operator-supplied manifest path
		if err != nil {
			return fmt.Errorf("k apply: --config renders a single file: %w", err)
		}
		rendered, err := renderManifest(string(raw), cl, st)
		if err != nil {
			return fmt.Errorf("k apply: rendering %s: %w", kApplyFilename, err)
		}
		opts.Filename = "-"
		opts.KubeconfigPath = cl.StateDir() + "/kubeconfig"
		opts.IOStreams.In = strings.NewReader(rendered)
	}
	return opts.Run(cmd.Context())
}

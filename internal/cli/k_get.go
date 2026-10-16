package cli

import (
	"os"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

var (
	kGetNamespace     string
	kGetAllNamespaces bool
	kGetLabelSelector string
	kGetOutput        string
)

func newKGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <resource> [name] [-n <ns> | -A] [-l <selector>] [-o <fmt>]",
		Short: "Get one or more resources (pods, nodes, services, CRDs, …)",
		Long: `Fetches Kubernetes resources via client-go, no host kubectl required.

The resource argument accepts plurals, singulars, and short names from
RESTMapper (pods/pod/po, services/svc, deployments/deploy). Multiple
types can be comma-separated:

  gkebnkctl k get pods,services -n f5-bnk
  gkebnkctl k get nodes -o yaml
  gkebnkctl k get pods -A -l app.kubernetes.io/name=f5-lifecycle-operator
  gkebnkctl k get pod my-pod -n default -o jsonpath='{.status.phase}'

CRDs work via dynamic discovery without a hardcoded list — ` + "`" + `gkebnkctl k get
cneinstances` + "`" + ` resolves the same way kubectl does.`,
		Args: cobra.MinimumNArgs(1),
		RunE: runKGet,
	}
	flags := cmd.Flags()
	flags.StringVarP(&kGetNamespace, "namespace", "n", "", "namespace scope (default: current-context's namespace)")
	flags.BoolVarP(&kGetAllNamespaces, "all-namespaces", "A", false, "list across all namespaces")
	flags.StringVarP(&kGetLabelSelector, "selector", "l", "", "label selector (e.g. 'app=foo,tier!=cache')")
	flags.StringVarP(&kGetOutput, "output", "o", "", "output format: yaml | json | wide | name | jsonpath=... | go-template=...")
	return cmd
}

func init() {
	kCmd.AddCommand(newKGetCmd())
}

func runKGet(cmd *cobra.Command, args []string) error {
	opts := &k8s.GetOptions{
		Args:          args,
		Namespace:     kGetNamespace,
		AllNamespaces: kGetAllNamespaces,
		LabelSelector: kGetLabelSelector,
		Output:        kGetOutput,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
	return opts.Run()
}

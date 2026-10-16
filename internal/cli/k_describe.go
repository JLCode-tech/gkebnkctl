package cli

import (
	"os"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

var (
	kDescribeNamespace     string
	kDescribeAllNamespaces bool
	kDescribeLabelSelector string
	kDescribeShowEvents    bool
)

func newKDescribeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe <resource> [name] [-n <ns> | -A] [-l <selector>]",
		Short: "Show detailed human-readable resource info (events, conditions, related objects)",
		Long: `Delegates to k8s.io/kubectl/pkg/describe — the same library kubectl/oc
use internally, so output is byte-equivalent.

Examples:

  gkebnkctl k describe pod my-pod -n f5-bnk
  gkebnkctl k describe nodes
  gkebnkctl k describe deployment f5-lifecycle-operator -n f5-bnk`,
		Args: cobra.MinimumNArgs(1),
		RunE: runKDescribe,
	}
	flags := cmd.Flags()
	flags.StringVarP(&kDescribeNamespace, "namespace", "n", "", "namespace scope")
	flags.BoolVarP(&kDescribeAllNamespaces, "all-namespaces", "A", false, "describe across all namespaces")
	flags.StringVarP(&kDescribeLabelSelector, "selector", "l", "", "label selector")
	flags.BoolVar(&kDescribeShowEvents, "show-events", true, "include the Events block (kubectl default: true)")
	return cmd
}

func init() {
	kCmd.AddCommand(newKDescribeCmd())
}

func runKDescribe(cmd *cobra.Command, args []string) error {
	opts := &k8s.DescribeOptions{
		Args:          args,
		Namespace:     kDescribeNamespace,
		AllNamespaces: kDescribeAllNamespaces,
		LabelSelector: kDescribeLabelSelector,
		ShowEvents:    kDescribeShowEvents,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
	return opts.Run()
}

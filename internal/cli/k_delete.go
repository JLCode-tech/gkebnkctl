package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

var (
	kDeleteNamespace     string
	kDeleteAllNamespaces bool
	kDeleteLabelSelector string
	kDeleteForce         bool
	kDeleteGracePeriod   int
	kDeleteCascade       string
)

func newKDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <resource> [name] [-n <ns> | -A] [-l <selector>] [--force] [--grace-period N] [--cascade orphan|background|foreground]",
		Short: "Delete resources by name or label selector",
		Long: `Deletes resources via the dynamic client. Cascade options match kubectl's:

  --cascade=background  delete the object; controller cleans dependents async (default)
  --cascade=foreground  block until dependents are gone
  --cascade=orphan      delete only the object, leave dependents

Examples:

  gkebnkctl k delete pod my-pod -n f5-bnk
  gkebnkctl k delete pods -l app=stale --force --grace-period=0
  gkebnkctl k delete deployment foo --cascade=foreground`,
		Args: cobra.MinimumNArgs(1),
		RunE: runKDelete,
	}
	flags := cmd.Flags()
	flags.StringVarP(&kDeleteNamespace, "namespace", "n", "", "namespace scope")
	flags.BoolVarP(&kDeleteAllNamespaces, "all-namespaces", "A", false, "delete across all namespaces")
	flags.StringVarP(&kDeleteLabelSelector, "selector", "l", "", "label selector")
	flags.BoolVar(&kDeleteForce, "force", false, "force-delete: implies --grace-period=0 unless overridden")
	flags.IntVar(&kDeleteGracePeriod, "grace-period", -1, "graceful termination period (seconds); -1 = use resource default")
	flags.StringVar(&kDeleteCascade, "cascade", "background", "cascade: orphan|background|foreground")
	return cmd
}

func init() {
	kCmd.AddCommand(newKDeleteCmd())
}

func runKDelete(cmd *cobra.Command, args []string) error {
	cascade := k8s.DeleteCascade(kDeleteCascade)
	switch cascade {
	case k8s.CascadeBackground, k8s.CascadeForeground, k8s.CascadeOrphan:
	default:
		return fmt.Errorf("invalid --cascade %q (orphan|background|foreground)", kDeleteCascade)
	}
	opts := &k8s.DeleteOptions{
		Args:          args,
		Namespace:     kDeleteNamespace,
		AllNamespaces: kDeleteAllNamespaces,
		LabelSelector: kDeleteLabelSelector,
		Force:         kDeleteForce,
		GracePeriod:   kDeleteGracePeriod,
		Cascade:       cascade,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
	return opts.Run(cmd.Context())
}

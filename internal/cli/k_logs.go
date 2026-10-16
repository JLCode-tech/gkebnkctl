package cli

import (
	"os"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

var (
	kLogsNamespace string
	kLogsContainer string
	kLogsFollow    bool
	kLogsPrevious  bool
	kLogsSince     string
	kLogsTail      int64
)

func newKLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <pod-name> [-n <ns>] [-c <container>] [-f] [--previous] [--since 5m] [--tail N]",
		Short: "Stream pod logs (kubectl-equivalent direct path)",
		Long: `Streams logs for a named pod. Differs from the top-level
'gkebnkctl logs <component>' in that this takes a literal pod name —
matching kubectl's surface — while the component variant maps a known
BNK component name to a label selector.

Both forms honour -n, -c, -f, --previous, --since, --tail.`,
		Args: cobra.ExactArgs(1),
		RunE: runKLogs,
	}
	flags := cmd.Flags()
	flags.StringVarP(&kLogsNamespace, "namespace", "n", "", "namespace scope (default: default)")
	flags.StringVarP(&kLogsContainer, "container", "c", "", "container name in a multi-container pod")
	flags.BoolVarP(&kLogsFollow, "follow", "f", false, "follow log output")
	flags.BoolVar(&kLogsPrevious, "previous", false, "fetch logs from the previous container instance")
	flags.StringVar(&kLogsSince, "since", "", "only return logs newer than this duration (e.g. 5s, 2m, 1h)")
	flags.Int64Var(&kLogsTail, "tail", -1, "tail the last N lines (-1 = full log)")
	return cmd
}

func init() {
	kCmd.AddCommand(newKLogsCmd())
}

func runKLogs(cmd *cobra.Command, args []string) error {
	since, err := k8s.ParseSinceDuration(kLogsSince)
	if err != nil {
		return err
	}
	opts := &k8s.LogsOptions{
		PodName:      args[0],
		Namespace:    kLogsNamespace,
		Container:    kLogsContainer,
		Follow:       kLogsFollow,
		Previous:     kLogsPrevious,
		SinceSeconds: since,
		TailLines:    kLogsTail,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
	return opts.Run(cmd.Context())
}

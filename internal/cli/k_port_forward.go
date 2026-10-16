package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

var kPortForwardNamespace string

func newKPortForwardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "port-forward <pod>|svc/<service> [-n <ns>] <local-port>[:<remote-port>] [...]",
		Aliases: []string{"port_forward"},
		Short:   "Forward local port(s) to a pod or a Service's pod via SPDY",
		Long: `Forwards one or more local TCP ports to ports on the named pod, or on a
Ready pod behind svc/<name> (Service ports map to their targetPort).
Equivalent to kubectl port-forward; signal handling closes the tunnel
cleanly on Ctrl+C.

Port spec:

  8080            local 8080 → pod 8080
  8080:80         local 8080 → pod 80
  :80             ephemeral local port → pod 80

Examples:

  gkebnkctl k port-forward my-pod 8080:80
  gkebnkctl k port-forward my-pod -n f5-bnk 9090:9090 8080:80
  gkebnkctl k port-forward -n llm-egress svc/loki 3100:3100`,
		Args: cobra.MinimumNArgs(2),
		RunE: runKPortForward,
	}
	cmd.Flags().StringVarP(&kPortForwardNamespace, "namespace", "n", "", "namespace scope (default: default)")
	return cmd
}

func init() {
	kCmd.AddCommand(newKPortForwardCmd())
}

func runKPortForward(cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: gkebnkctl k port-forward <pod> <local>:<remote> [...]")
	}
	pod := args[0]
	ports := args[1:]
	opts := &k8s.PortForwardOptions{
		PodName:   pod,
		Namespace: kPortForwardNamespace,
		Ports:     ports,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
	return opts.Run(cmd.Context())
}

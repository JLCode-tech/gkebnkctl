package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

var (
	kExecNamespace string
	kExecContainer string
	kExecStdin     bool
	kExecTTY       bool
)

func newKExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <pod> [-n <ns>] [-c <container>] [-i] [-t] -- <cmd> [args...]",
		Short: "Exec into a pod via SPDY (kubectl-equivalent in-process)",
		Long: `Opens an exec stream against the named pod over SPDY. The semantics
mirror kubectl exec:

  -i / --stdin       attach stdin to the remote process
  -t / --tty         allocate a PTY (use for top, bash-style interactive work)
  -c / --container   pick a container in a multi-container pod

Examples:

  gkebnkctl k exec my-pod -- ls /tmp
  gkebnkctl k exec my-pod -it -- bash
  gkebnkctl k exec my-pod -c sidecar -- cat /etc/hostname`,
		Args:               cobra.MinimumNArgs(2), // pod + at least one cmd token
		DisableFlagParsing: false,
		RunE:               runKExec,
	}
	flags := cmd.Flags()
	flags.StringVarP(&kExecNamespace, "namespace", "n", "", "namespace scope (default: default)")
	flags.StringVarP(&kExecContainer, "container", "c", "", "container name in a multi-container pod")
	flags.BoolVarP(&kExecStdin, "stdin", "i", false, "attach stdin")
	flags.BoolVarP(&kExecTTY, "tty", "t", false, "allocate a PTY")
	return cmd
}

func init() {
	kCmd.AddCommand(newKExecCmd())
}

func runKExec(cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: gkebnkctl k exec <pod> -- <cmd> [args...]")
	}
	pod := args[0]
	command := args[1:]

	// Honour the canonical `--` if present (cobra strips it from args
	// when DisableFlagParsing is false; this is a defensive cleanup
	// for the rare case it survives).
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		return errors.New("command required after pod name")
	}

	// If --tty and stdin is a terminal, stash and restore raw mode so
	// the remote PTY drives keypress semantics.
	if kExecTTY {
		if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
			oldState, err := term.MakeRaw(fd)
			if err == nil {
				defer term.Restore(fd, oldState)
			}
		}
	}

	opts := &k8s.ExecOptions{
		PodName:   pod,
		Namespace: kExecNamespace,
		Container: kExecContainer,
		Stdin:     kExecStdin,
		TTY:       kExecTTY,
		Command:   command,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
	return opts.Run(cmd.Context())
}

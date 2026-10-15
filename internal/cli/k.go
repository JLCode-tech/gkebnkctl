package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/kubeconfig"
)

var kCmd = &cobra.Command{
	Use:   "k [kubectl arguments...]",
	Short: "Scoped kubectl wrapper targeting the workspace cluster",
	Long: `gkebnkctl k runs kubectl commands targeted directly at your GKE BNK cluster
without modifying your global ~/.kube/config or requiring manual context switching.

It uses artifacts/kubeconfig if present, or dynamically fetches credentials from GKE.

Example:
  gkebnkctl k get pods -A
  gkebnkctl k get cneinstances,f5spkvlans -n f5-cne-system
  gkebnkctl k logs -n f5-cne-system daemonset/f5-tmm -c tmm
  gkebnkctl k exec -it -n egress-test egress-client -- curl -v http://10.30.0.1`,
	DisableFlagParsing: true,
	RunE:               runK,
}

func init() {
	rootCmd.AddCommand(kCmd)
}

func runK(cmd *cobra.Command, args []string) error {
	kubectlBin, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not found in PATH: please install kubectl or run 'gkebnkctl doctor'")
	}

	// 1. Check if artifacts/kubeconfig exists
	kcPath := filepath.Join("artifacts", "kubeconfig")
	if _, err := os.Stat(kcPath); os.IsNotExist(err) {
		// Try generating it from cluster.yaml if cluster.yaml exists
		if fileExists("cluster.yaml") {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			cl, err := intent.Load("cluster.yaml")
			if err == nil {
				data, err := kubeconfig.FetchAndGenerate(ctx, cl)
				if err == nil {
					_ = kubeconfig.WriteFile(kcPath, data)
				}
			}
		}
	}

	var execArgs []string
	if fileExists(kcPath) {
		absKC, _ := filepath.Abs(kcPath)
		execArgs = append(execArgs, "--kubeconfig", absKC)
	}

	execArgs = append(execArgs, args...)

	proc := exec.Command(kubectlBin, execArgs...) // #nosec G204 -- kubectl wrapper forwards operator CLI args
	proc.Stdin = os.Stdin
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr

	if err := proc.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}

	return nil
}

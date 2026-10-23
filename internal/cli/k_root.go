package cli

import "github.com/spf13/cobra"

// kCmd is the parent command for kubectl-internalised verbs. The
// disambiguation pattern: host-side `gkebnkctl exec` stays as-is;
// cluster-side equivalent is `gkebnkctl k exec`. Same shape for
// `k get`, `k apply`, `k logs`, `k describe`, `k delete`, `k port-forward`.
//
// Top-level shortcuts for the most common verbs (`get`, `logs`) are added in
// k_aliases.go so users don't need to learn the `k` prefix.
var kCmd = &cobra.Command{
	Use:   "k",
	Short: "Kubernetes verbs (kubectl-internalised; no host kubectl required)",
	Long: `gkebnkctl k <verb> runs the BNK-relevant kubectl verb subset
natively in-process via client-go, with no host kubectl binary
required. Output formatting matches kubectl byte-for-byte for
-o yaml/json/wide/name/jsonpath/go-template.

Verbs:
  k get          list/show resources
  k describe     human-friendly resource detail (delegates to kubectl/pkg/describe)
  k apply        server-side apply for files, dirs, kustomize bases, or stdin
  k delete       delete with cascade + grace period control
  k logs         pod logs
  k exec         exec into a pod via SPDY
  k port-forward forward a local port to a pod via SPDY`,
}

func init() {
	rootCmd.AddCommand(kCmd)
}

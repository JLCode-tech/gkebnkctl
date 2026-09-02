package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

var flagVersionJSON bool

type VersionInfo struct {
	Version           string `json:"version"`
	GitCommit         string `json:"gitCommit"`
	BuildDate         string `json:"buildDate"`
	GoVersion         string `json:"goVersion"`
	Platform          string `json:"platform"`
	BnkManifest       string `json:"bnkManifestPin"`
	CertManager       string `json:"certManagerPin"`
	KubernetesVersion string `json:"kubernetesRecommended"`
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print gkebnkctl version, build metadata, and pinned component versions",
	Long: `gkebnkctl version displays the binary release version, git commit, build date,
Go compiler version, and pinned F5 BNK / GKE component manifests.

Example:
  gkebnkctl version
  gkebnkctl version --json`,
	RunE: runVersion,
}

func init() {
	versionCmd.Flags().BoolVar(&flagVersionJSON, "json", false, "Output version information in JSON format")
	rootCmd.AddCommand(versionCmd)
}

func runVersion(_ *cobra.Command, _ []string) error {
	info := VersionInfo{
		Version:           Version,
		GitCommit:         Commit,
		BuildDate:         BuildDate,
		GoVersion:         runtime.Version(),
		Platform:          fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		BnkManifest:       "2.4.0-3.2882.0",
		CertManager:       "v1.16.1",
		KubernetesVersion: "1.35",
	}

	if flagVersionJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}

	fmt.Printf("gkebnkctl version %s\n", info.Version)
	fmt.Printf("  Commit:     %s\n", info.GitCommit)
	fmt.Printf("  Built:      %s\n", info.BuildDate)
	fmt.Printf("  Go:         %s\n", info.GoVersion)
	fmt.Printf("  Platform:   %s\n", info.Platform)
	fmt.Printf("\nPinned Components:\n")
	fmt.Printf("  F5 BNK Manifest:    %s\n", info.BnkManifest)
	fmt.Printf("  cert-manager:       %s\n", info.CertManager)
	fmt.Printf("  GKE Kubernetes:     %s\n", info.KubernetesVersion)

	return nil
}

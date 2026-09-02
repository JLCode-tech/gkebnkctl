package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

var (
	flagInitProject  string
	flagInitName     string
	flagInitRegion   string
	flagInitZone     string
	flagInitMainCIDR string
	flagInitExtCIDR  string
	flagInitIntCIDR  string
	flagInitForce    bool
)

var initCmd = &cobra.Command{
	Use:   "init [directory]",
	Short: "Scaffold a new gkebnkctl cluster workspace (cluster.yaml, keys/, artifacts/, AGENTS.md)",
	Long: `gkebnkctl init creates a new deployment workspace ready for provisioning:
  - cluster.yaml (declarative cluster specification)
  - keys/ (gitignored directory for license.jwt and FAR auth archive)
  - artifacts/ (workspace output directory for kubeconfig and reports)
  - AGENTS.md (quick-start reference for AI agents and operators)
  - .env (local environment file)

Example:
  gkebnkctl init poc-gke --project my-gcp-project --name bnk-cluster-01
  gkebnkctl init . --project my-gcp-project`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&flagInitProject, "project", "", "GCP project ID (required)")
	initCmd.Flags().StringVar(&flagInitName, "name", "", "Cluster name (default: directory name)")
	initCmd.Flags().StringVar(&flagInitRegion, "region", "us-central1", "GCP region")
	initCmd.Flags().StringVar(&flagInitZone, "zone", "us-central1-a", "GCP zone")
	initCmd.Flags().StringVar(&flagInitMainCIDR, "main-cidr", "10.60.0.0/24", "Main VPC subnet CIDR")
	initCmd.Flags().StringVar(&flagInitExtCIDR, "ext-cidr", "10.30.0.0/24", "External dataplane VPC CIDR")
	initCmd.Flags().StringVar(&flagInitIntCIDR, "int-cidr", "10.50.0.0/24", "Internal dataplane VPC CIDR")
	initCmd.Flags().BoolVarP(&flagInitForce, "force", "f", false, "Overwrite existing files if present")

	rootCmd.AddCommand(initCmd)
}

func runInit(_ *cobra.Command, args []string) error {
	targetDir := "."
	if len(args) > 0 {
		targetDir = args[0]
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolve target dir: %w", err)
	}

	dirName := filepath.Base(absDir)
	clusterName := flagInitName
	if clusterName == "" {
		if dirName == "." || dirName == "/" {
			clusterName = "bnk-hostdev"
		} else {
			clusterName = sanitizeName(dirName)
		}
	}

	project := flagInitProject
	if project == "" {
		project = "YOUR_GCP_PROJECT_ID"
	}

	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return fmt.Errorf("create workspace directory: %w", err)
	}

	// 1. Write cluster.yaml
	clusterFile := filepath.Join(absDir, "cluster.yaml")
	if !flagInitForce && fileExists(clusterFile) {
		return fmt.Errorf("%s already exists (pass -f / --force to overwrite)", clusterFile)
	}

	clusterConfig := intent.Cluster{
		APIVersion: intent.APIVersion,
		Kind:       intent.Kind,
		Metadata: intent.Metadata{
			Name:    clusterName,
			Project: project,
			Zone:    flagInitZone,
			Labels: map[string]string{
				"purpose": "bnk-hostdev-demo",
			},
		},
		Pattern: intent.PatternHostDevice,
		Network: intent.Network{
			Main: intent.MainNetworkSpec{
				VPC:           clusterName + "-main-vpc",
				Subnet:        clusterName + "-main-subnet",
				CIDR:          flagInitMainCIDR,
				PodsRangeName: clusterName + "-pods",
				PodsCIDR:      "10.66.0.0/16",
				SvcsRangeName: clusterName + "-svcs",
				SvcsCIDR:      "10.67.0.0/20",
			},
			External: intent.DataplaneSpec{
				VPC:           "shared-ext-vpc",
				Subnet:        "shared-ext-subnet",
				CIDR:          flagInitExtCIDR,
				MTU:           8896,
				PodsRangeName: "tmm-ext-pods",
				PodsCIDR:      "10.31.0.0/24",
				UnderlayCIDR:  "10.30.0.0/16",
			},
			Internal: intent.DataplaneSpec{
				VPC:           "shared-int-vpc",
				Subnet:        "shared-int-subnet",
				CIDR:          flagInitIntCIDR,
				MTU:           8896,
				PodsRangeName: "tmm-int-pods",
				PodsCIDR:      "10.51.0.0/24",
			},
			AllowInternalCidr: "10.0.0.0/8",
		},
		ClusterSpec: intent.ClusterSpec{
			KubernetesVersion: "1.35",
			DefaultPool: intent.NodePoolSpec{
				MachineType: "e2-standard-4",
				NodeCount:   3,
			},
			TmmPool: intent.NodePoolSpec{
				Name:           "tmm-pool",
				MachineType:    "e2-standard-8",
				NodeCount:      1,
				MaxPodsPerNode: 8,
			},
			AgentPool: intent.NodePoolSpec{
				Name:           "agent-pool",
				MachineType:    "e2-standard-4",
				NodeCount:      1,
				MaxPodsPerNode: 8,
			},
		},
		Bnk: intent.BnkSpec{
			ManifestVersion: "2.4.0-3.2882.0",
			TmmMtu:          "1460",
			JwtPath:         "./keys/license.jwt",
			FarSecret: intent.FarSecretSpec{
				Source:           "dockerconfig",
				DockerConfigFile: "./keys/dockerconfig.json",
				Namespaces:       []string{"f5-bnk", "f5-utils"},
			},
		},
		Egress: intent.EgressSpec{
			TestNamespace: "egress-test",
			VxlanPort:     5789,
			VxlanKey:      100,
			VxlanMtu:      1410,
		},
	}

	yamlData, err := yaml.Marshal(&clusterConfig)
	if err != nil {
		return fmt.Errorf("marshal cluster.yaml: %w", err)
	}

	header := []byte("# Generated by gkebnkctl init\n# Edit this file to customize your GKE BNK deployment.\n\n")
	if err := os.WriteFile(clusterFile, append(header, yamlData...), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", clusterFile, err)
	}

	// 2. Setup keys/ directory with local .gitignore
	keysDir := filepath.Join(absDir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return fmt.Errorf("create keys dir: %w", err)
	}

	keysGitignore := filepath.Join(keysDir, ".gitignore")
	gitignoreContent := "# Ignore all secret keys and auth tokens\n*\n!.gitignore\n!README.md\n"
	_ = os.WriteFile(keysGitignore, []byte(gitignoreContent), 0o600)

	keysReadme := filepath.Join(keysDir, "README.md")
	readmeContent := "# Secret Material Directory\n\n" +
		"Place your sensitive credentials here:\n" +
		"- `license.jwt`: Your F5 BNK subscription JWT license.\n" +
		"- `f5-far-auth-key.tgz`: Your F5 FAR registry auth key archive.\n\n" +
		"This directory is protected by `.gitignore` to prevent accidental commits.\n"
	_ = os.WriteFile(keysReadme, []byte(readmeContent), 0o600)

	// 3. Setup artifacts/ directory
	artifactsDir := filepath.Join(absDir, "artifacts")
	_ = os.MkdirAll(artifactsDir, 0o750)

	// 4. Write AGENTS.md guide
	agentsFile := filepath.Join(absDir, "AGENTS.md")
	agentsContent := fmt.Sprintf("# Operator & Agent Quick-Start Guide for %s\n\n"+
		"This workspace defines an F5 BIG-IP Next for Kubernetes (BNK) deployment on Google Kubernetes Engine (GKE).\n\n"+
		"## 4-Command Lifecycle\n\n"+
		"1. **Preflight Check**:\n"+
		"   ```bash\n"+
		"   gkebnkctl doctor --project %s --region %s\n"+
		"   ```\n\n"+
		"2. **Validate Configuration**:\n"+
		"   ```bash\n"+
		"   gkebnkctl validate cluster.yaml\n"+
		"   ```\n\n"+
		"3. **Deploy (Plan & Provision)**:\n"+
		"   ```bash\n"+
		"   gkebnkctl up -f cluster.yaml --dry-run\n"+
		"   gkebnkctl up -f cluster.yaml\n"+
		"   ```\n\n"+
		"4. **Connect via Kubeconfig**:\n"+
		"   ```bash\n"+
		"   gkebnkctl kubeconfig -f cluster.yaml --write-global\n"+
		"   ```\n\n"+
		"5. **Run Tests**:\n"+
		"   ```bash\n"+
		"   gkebnkctl test dns --target f5.com\n"+
		"   ```\n\n"+
		"6. **Teardown**:\n"+
		"   ```bash\n"+
		"   gkebnkctl down -f cluster.yaml\n"+
		"   ```\n", clusterName, project, flagInitRegion)
	_ = os.WriteFile(agentsFile, []byte(agentsContent), 0o600)

	// 5. Write .env helper
	dotenvFile := filepath.Join(absDir, ".env")
	if !fileExists(dotenvFile) {
		envContent := fmt.Sprintf("# Local environment configuration\nCLOUDSDK_CORE_PROJECT=%s\n", project)
		_ = os.WriteFile(dotenvFile, []byte(envContent), 0o600)
	}

	fmt.Fprintf(os.Stdout, "✓ Initialized gkebnkctl workspace in %s\n", absDir)
	fmt.Fprintf(os.Stdout, "  • Created cluster.yaml (cluster=%s, project=%s)\n", clusterName, project)
	fmt.Fprintf(os.Stdout, "  • Created keys/ (drop license.jwt and f5-far-auth-key.tgz here)\n")
	fmt.Fprintf(os.Stdout, "  • Created artifacts/ and AGENTS.md\n\n")
	fmt.Fprintf(os.Stdout, "Next steps:\n")
	fmt.Fprintf(os.Stdout, "  1. Drop your license in keys/license.jwt\n")
	fmt.Fprintf(os.Stdout, "  2. Run: gkebnkctl doctor --project %s\n", project)
	fmt.Fprintf(os.Stdout, "  3. Run: gkebnkctl up -f %s/cluster.yaml\n", targetDir)

	return nil
}

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return "bnk-hostdev"
	}
	return res
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

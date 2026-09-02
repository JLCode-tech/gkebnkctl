package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// TargetHarness represents an AI assistant harness type.
type TargetHarness string

const (
	HarnessGemini   TargetHarness = "gemini"
	HarnessClaude   TargetHarness = "claude"
	HarnessOpencode TargetHarness = "opencode"
	HarnessCursor   TargetHarness = "cursor"
	HarnessAll      TargetHarness = "all"
)

// Generate scaffolds agent instruction files for the specified harness.
func Generate(harness TargetHarness, clusterFile string, force bool) ([]string, error) {
	var cl *intent.Cluster
	if clusterFile != "" && fileExists(clusterFile) {
		loaded, err := intent.Load(clusterFile)
		if err == nil {
			cl = loaded
		}
	}

	clusterName := "bnk-cluster"
	project := "YOUR_GCP_PROJECT"
	region := "us-central1"
	if cl != nil {
		clusterName = cl.Metadata.Name
		project = cl.Metadata.Project
		region = cl.Region
	}

	var generated []string

	if harness == HarnessAll || harness == HarnessGemini || harness == HarnessClaude {
		agentsContent := renderAgentsMD(clusterName, project, region)
		if err := writeDoc("AGENTS.md", agentsContent, force); err != nil {
			return nil, err
		}
		generated = append(generated, "AGENTS.md")
	}

	if harness == HarnessAll || harness == HarnessClaude {
		claudeContent := renderClaudeMD(clusterName, project, region)
		if err := writeDoc("CLAUDE.md", claudeContent, force); err != nil {
			return nil, err
		}
		generated = append(generated, "CLAUDE.md")
	}

	if harness == HarnessAll || harness == HarnessCursor {
		cursorContent := renderCursorRules(clusterName, project, region)
		if err := writeDoc(".cursorrules", cursorContent, force); err != nil {
			return nil, err
		}
		generated = append(generated, ".cursorrules")
	}

	return generated, nil
}

func renderAgentsMD(name, project, region string) string {
	return fmt.Sprintf(`# Operator & AI Agent Quick-Start: %s

## Mission
You are assisting the operator in managing the F5 BIG-IP Next for Kubernetes (BNK) deployment on Google Kubernetes Engine (GKE).

## Deployment Topology
- **Cluster Name**: %s
- **GCP Project**: %s
- **Region**: %s
- **Data Plane**: Dual-stack Multi-Networking (host-device interfaces)

## 4-Command Core Lifecycle
1. Preflight Diagnostics:
   `+"```bash"+`
   gkebnkctl doctor --project %s
   `+"```"+`
2. Configuration Validation:
   `+"```bash"+`
   gkebnkctl validate cluster.yaml
   `+"```"+`
3. Provision & Deploy:
   `+"```bash"+`
   gkebnkctl up -f cluster.yaml --dry-run
   gkebnkctl up -f cluster.yaml
   `+"```"+`
4. Day-2 & Verification:
   `+"```bash"+`
   gkebnkctl k get pods -A
   gkebnkctl test dns --target f5.com
   gkebnkctl test egress -f cluster.yaml
   `+"```"+`
5. Teardown:
   `+"```bash"+`
   gkebnkctl down -f cluster.yaml
   `+"```"+`
`, name, name, project, region, project)
}

func renderClaudeMD(name, project, region string) string {
	return fmt.Sprintf(`# Claude Code Instructions for %s

- Primary CLI: `+"`gkebnkctl`"+`
- Project: `+"`%s`"+` (GKE Region: `+"`%s`"+`)
- Inspect Cluster: `+"`gkebnkctl k get pods -A`"+`
- Run Tests: `+"`gkebnkctl test dns --target f5.com`"+`
- Read Logs: `+"`gkebnkctl journal list`"+`
`, name, project, region)
}

func renderCursorRules(name, project, region string) string {
	return fmt.Sprintf(`# Cursor Rules for %s (GCP Project: %s, Region: %s)
- Use gkebnkctl for all cluster lifecycle operations.
- Prefer gkebnkctl k over raw kubectl.
- Append notes with gkebnkctl journal add "<note>".
`, name, project, region)
}

func writeDoc(path, content string, force bool) error {
	if !force && fileExists(path) {
		return fmt.Errorf("%s already exists (pass --force to overwrite)", path)
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

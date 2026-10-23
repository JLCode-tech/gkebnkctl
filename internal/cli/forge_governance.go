package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

var (
	govClusterID   int
	govClusterName string
	govForgeURL    string
	govAsJSON      bool
)

var forgeGovernanceCmd = &cobra.Command{
	Use:   "governance",
	Short: "Multi-Cluster AI Gateway & Dependency Fabric operations via BNK-Forge",
}

func resolveForgeGovernanceParams(cl *intent.Cluster) (string, forge.RestCreds) {
	var fs *intent.ForgeSpec
	if cl != nil {
		fs = cl.Forge
	}
	restURL := fs.ResolveURL()
	username := fs.ResolveUsername()
	password, _ := fs.ResolvePassword()
	return restURL, forge.RestCreds{Username: username, Password: password}
}

func resolveForgeGovernanceTarget(cmd *cobra.Command) (int, string, forge.RestCreds, error) {
	cfgPath := flagForgeConfig
	if cfgPath == "" {
		cfgPath = "cluster.yaml"
	}
	var cl *intent.Cluster
	var st *state.State
	if loaded, err := intent.Load(cfgPath); err == nil {
		cl = loaded
		if loadedSt, stErr := state.Load(cl.StateDir()); stErr == nil {
			st = loadedSt
		}
	}

	effRESTURL, creds := resolveForgeGovernanceParams(cl)
	if strings.TrimSpace(govForgeURL) != "" {
		effRESTURL = strings.TrimSpace(govForgeURL)
	}

	wsDir := "."
	if cl != nil && cl.StateDir() != "" {
		wsDir = cl.StateDir()
	}

	cid := govClusterID
	if link, lErr := forge.ReadLink(wsDir); lErr == nil && link != nil {
		if cid <= 0 {
			cid = link.ClusterID
		}
		if effRESTURL == "" && link.ForgeURL != "" {
			effRESTURL = link.ForgeURL
		}
	}
	if cid <= 0 && st != nil {
		cid, _ = strconv.Atoi(st.Get("FORGE_CLUSTER_ID"))
	}
	if cid <= 0 {
		lookupName := strings.TrimSpace(govClusterName)
		if lookupName == "" && cl != nil {
			lookupName = cl.Metadata.Name
		}
		if lookupName != "" {
			if resolvedID, rErr := forge.ResolveClusterIDByName(cmd.Context(), effRESTURL, lookupName, creds); rErr == nil && resolvedID > 0 {
				cid = resolvedID
			}
		}
	}
	if cid <= 0 {
		return 0, effRESTURL, creds, fmt.Errorf("forge cluster ID not found (pass --cluster-id, --cluster-name, or run `gkebnkctl forge register` first)")
	}
	return cid, effRESTURL, creds, nil
}

func init() {
	forgeGovernanceCmd.PersistentFlags().IntVar(&govClusterID, "cluster-id", 0, "Forge cluster ID (defaults to workspace forge_link.json)")
	forgeGovernanceCmd.PersistentFlags().StringVar(&govClusterName, "cluster-name", "", "Forge cluster name (fallback when --cluster-id or forge_link.json is absent)")
	forgeGovernanceCmd.PersistentFlags().StringVar(&govForgeURL, "forge-url", "", "BNK-Forge REST base URL override")
	forgeGovernanceCmd.PersistentFlags().BoolVar(&govAsJSON, "json", false, "Emit raw JSON output")

	govOverviewCmd := &cobra.Command{
		Use:   "overview",
		Short: "Fetch the Multi-Cluster AI Gateway dependency graph and readiness checklist",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cid, effRESTURL, creds, err := resolveForgeGovernanceTarget(cmd)
			if err != nil {
				return err
			}
			res, err := forge.GovernanceOverview(cmd.Context(), effRESTURL, cid, creds)
			if err != nil {
				return err
			}
			if govAsJSON {
				b, _ := json.MarshalIndent(res, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), res.Summary())
			return nil
		},
	}

	var remAction, remNamespace, remTargetName string
	govRemediateCmd := &cobra.Command{
		Use:   "remediate [action]",
		Short: "Trigger a BNK 2.4.1 AI Gateway remediation action in BNK-Forge",
		RunE: func(cmd *cobra.Command, args []string) error {
			action := strings.TrimSpace(remAction)
			if action == "" && len(args) > 0 {
				action = strings.TrimSpace(args[0])
			}
			if action == "" {
				return fmt.Errorf("remediation action is required (pass positional <action> or --action)")
			}
			cid, effRESTURL, creds, err := resolveForgeGovernanceTarget(cmd)
			if err != nil {
				return err
			}
			res, err := forge.GovernanceRemediate(cmd.Context(), effRESTURL, cid, forge.GovernanceRemediateRequest{
				Action:     action,
				Namespace:  remNamespace,
				TargetName: remTargetName,
			}, creds)
			if err != nil {
				return err
			}
			if govAsJSON {
				b, _ := json.MarshalIndent(res, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "remediate action=%s status=%s success=%v message=%s\n", res.Action, res.Status, res.Success, res.Message)
			return nil
		},
	}
	govRemediateCmd.Flags().StringVar(&remAction, "action", "", "Remediation action name (e.g., restart_cne_controller, sync_dns_cache, ensure_default_route, patch_semantic_cache)")
	govRemediateCmd.Flags().StringVarP(&remNamespace, "namespace", "n", "f5-cne-system", "Target Kubernetes namespace for remediation")
	govRemediateCmd.Flags().StringVar(&remTargetName, "target-name", "", "Target resource name for remediation")

	var probeVIP, probePath, probeModel, probeModelID, probeRouteName, probeNamespace, probeVirtualKey, probePrompt string
	var probePort, probeMaxTokens int
	var probeSimulateFailover bool
	var probeHeaders []string
	govTestReqCmd := &cobra.Command{
		Use:   "test-request",
		Short: "Send a live chat-completion test probe through the cluster's AI Gateway VIP via BNK-Forge",
		RunE: func(cmd *cobra.Command, _ []string) error {
			effModel := probeModel
			if effModel == "" {
				effModel = probeModelID
			}
			effModelID := probeModelID
			if effModelID == "" {
				effModelID = probeModel
			}
			cid, effRESTURL, creds, err := resolveForgeGovernanceTarget(cmd)
			if err != nil {
				return err
			}
			hdrMap := map[string]string{}
			for _, h := range probeHeaders {
				if k, v, ok := strings.Cut(h, "="); ok {
					hdrMap[strings.TrimSpace(k)] = strings.TrimSpace(v)
				} else if k, v, ok := strings.Cut(h, ":"); ok {
					hdrMap[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			vk := strings.TrimSpace(probeVirtualKey)
			if vk == "" {
				for k, v := range hdrMap {
					if strings.EqualFold(k, "Authorization") {
						trimmed := strings.TrimSpace(v)
						if strings.HasPrefix(strings.ToLower(trimmed), "bearer ") {
							vk = strings.TrimSpace(trimmed[7:])
						} else {
							vk = trimmed
						}
					}
				}
			}
			if vk == "" {
				vk = strings.TrimSpace(os.Getenv("AIGW_VIRTUAL_KEY"))
			}
			if vk == "" {
				vk = strings.TrimSpace(os.Getenv("BNKCTL_VIRTUAL_KEY"))
			}
			res, err := forge.GovernanceTestRequest(cmd.Context(), effRESTURL, cid, forge.GovernanceTestRequestPayload{
				RouteName:              probeRouteName,
				Namespace:              probeNamespace,
				VirtualKey:             vk,
				Prompt:                 probePrompt,
				Model:                  effModel,
				ModelID:                effModelID,
				SimulatePrimaryFailure: probeSimulateFailover,
				Path:                   probePath,
			}, creds)
			if err != nil {
				return err
			}
			if govAsJSON {
				b, _ := json.MarshalIndent(res, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), res.Summary())
			if res.ResponseText != "" {
				fmt.Fprintln(cmd.OutOrStdout(), res.ResponseText)
			} else if bodyStr := res.ResponseBodyString(); bodyStr != "" {
				fmt.Fprintln(cmd.OutOrStdout(), bodyStr)
			}
			return nil
		},
	}
	govTestReqCmd.Flags().StringVar(&probeRouteName, "route-name", "", "Target AIRoute / HTTPRoute name")
	govTestReqCmd.Flags().StringVarP(&probeNamespace, "namespace", "n", "f5-cne-system", "Target route namespace")
	govTestReqCmd.Flags().StringVar(&probeVirtualKey, "virtual-key", "", "DSSM Virtual API key (f5vk_...)")
	govTestReqCmd.Flags().BoolVar(&probeSimulateFailover, "simulate-failover", false, "Simulate primary model failure to test fallback routing")
	govTestReqCmd.Flags().StringVar(&probeVIP, "gateway-ip", "", "AI Gateway VIP address")
	govTestReqCmd.Flags().IntVar(&probePort, "port", 80, "AI Gateway listener port")
	govTestReqCmd.Flags().StringVar(&probePath, "path", "/v1/chat/completions", "Inference request path")
	govTestReqCmd.Flags().StringVar(&probeModel, "model", "", "Target AIModel name")
	govTestReqCmd.Flags().StringVar(&probeModelID, "model-id", "", "Explicit model ID override")
	govTestReqCmd.Flags().StringVar(&probePrompt, "prompt", "Summarize enterprise AI Gateway readiness in one sentence.", "Prompt message")
	govTestReqCmd.Flags().IntVar(&probeMaxTokens, "max-tokens", 128, "Maximum completion tokens")
	govTestReqCmd.Flags().StringSliceVarP(&probeHeaders, "header", "H", nil, "Additional request headers (Key=Value)")

	var bootTenant, bootOrgID, bootTeamID, bootPrincipalID, bootKeyName, bootModel string
	var bootRPM, bootTPM int
	var bootBudget float64
	govBootstrapCmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Provision DSSM AI Gateway org, team, principal, and virtual API key (f5vk_...) via BNK-Forge",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cid, effRESTURL, creds, err := resolveForgeGovernanceTarget(cmd)
			if err != nil {
				return err
			}
			var allowedModels []string
			if strings.TrimSpace(bootModel) != "" {
				allowedModels = []string{strings.TrimSpace(bootModel)}
			}
			orgRes, err := forge.GovernanceCreateOrg(cmd.Context(), effRESTURL, cid, forge.GovernanceOrgCreateRequest{
				Tenant:           bootTenant,
				Name:             bootOrgID,
				MonthlyBudgetUSD: bootBudget,
				RPMLimit:         bootRPM,
				TPMLimit:         bootTPM,
			}, creds)
			if err != nil {
				return fmt.Errorf("create org: %w", err)
			}
			orgID := orgRes.ID
			if orgID == "" {
				orgID = bootOrgID
			}
			if bootBudget > 0 {
				_, _ = forge.GovernanceCreateBudget(cmd.Context(), effRESTURL, cid, forge.GovernanceBudgetCreateRequest{
					Tenant:   bootTenant,
					Name:     bootOrgID + "-monthly",
					Scope:    "org",
					TargetID: orgID,
					LimitUSD: bootBudget,
					Period:   "monthly",
				}, creds)
			}
			teamRes, err := forge.GovernanceCreateTeam(cmd.Context(), effRESTURL, cid, forge.GovernanceTeamCreateRequest{
				Tenant:        bootTenant,
				OrgID:         orgID,
				Name:          bootTeamID,
				AllowedModels: allowedModels,
				RPMLimit:      bootRPM,
				TPMLimit:      bootTPM,
			}, creds)
			if err != nil {
				return fmt.Errorf("create team: %w", err)
			}
			teamID := teamRes.ID
			if teamID == "" {
				teamID = bootTeamID
			}
			prinRes, err := forge.GovernanceCreatePrincipal(cmd.Context(), effRESTURL, cid, forge.GovernancePrincipalCreateRequest{
				Tenant:        bootTenant,
				TeamID:        teamID,
				Name:          bootPrincipalID,
				Kind:          "agent",
				Role:          "developer",
				AllowedModels: allowedModels,
				RPMLimit:      bootRPM,
				TPMLimit:      bootTPM,
			}, creds)
			if err != nil {
				return fmt.Errorf("create principal: %w", err)
			}
			principalID := prinRes.ID
			if principalID == "" {
				principalID = bootPrincipalID
			}
			if bootRPM > 0 || bootTPM > 0 {
				_, _ = forge.GovernanceCreateRateLimit(cmd.Context(), effRESTURL, cid, forge.GovernanceRateLimitCreateRequest{
					Tenant:            bootTenant,
					Name:              bootPrincipalID + "-ratelimit",
					Scope:             "principal",
					TargetID:          principalID,
					RequestsPerMinute: bootRPM,
					TokensPerMinute:   bootTPM,
				}, creds)
			}
			keyRes, err := forge.GovernanceCreateVirtualKey(cmd.Context(), effRESTURL, cid, forge.GovernanceVirtualKeyCreateRequest{
				Tenant:           bootTenant,
				PrincipalID:      principalID,
				Name:             bootKeyName,
				AllowedModels:    allowedModels,
				RPMLimit:         bootRPM,
				TPMLimit:         bootTPM,
				MonthlyBudgetUSD: bootBudget,
			}, creds)
			if err != nil {
				return fmt.Errorf("create virtual key: %w", err)
			}
			if govAsJSON {
				b, _ := json.MarshalIndent(keyRes, "", "  ") // #nosec G117 -- intentional CLI JSON output of newly created key
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "tenant=%s org=%s team=%s principal=%s key_id=%s api_key=%s cleartext_key=%s\n",
				bootTenant, orgID, teamID, principalID, keyRes.KeyID, keyRes.APIKey, keyRes.CleartextKey)
			return nil
		},
	}
	govBootstrapCmd.Flags().StringVar(&bootTenant, "tenant", "default-tenant", "AITenant name")
	govBootstrapCmd.Flags().StringVar(&bootOrgID, "org", "f5-org", "DSSM Organization name/ID")
	govBootstrapCmd.Flags().StringVar(&bootTeamID, "team", "platform-ai", "DSSM Team name/ID")
	govBootstrapCmd.Flags().StringVar(&bootPrincipalID, "principal", "demo-agent", "DSSM Principal name/ID")
	govBootstrapCmd.Flags().StringVar(&bootKeyName, "key-name", "demo-agent-key", "Virtual API key name")
	govBootstrapCmd.Flags().StringVar(&bootModel, "model", "", "Allowed model ID for the virtual key")
	govBootstrapCmd.Flags().Float64Var(&bootBudget, "budget-usd", 1000.0, "Monthly organization budget in USD")
	govBootstrapCmd.Flags().IntVar(&bootRPM, "rpm-limit", 60, "Requests per minute limit")
	govBootstrapCmd.Flags().IntVar(&bootTPM, "tpm-limit", 100000, "Tokens per minute limit")

	forgeGovernanceCmd.AddCommand(govOverviewCmd, govRemediateCmd, govTestReqCmd, govBootstrapCmd)
	forgeCmd.AddCommand(forgeGovernanceCmd)
}

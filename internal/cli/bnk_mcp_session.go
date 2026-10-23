package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/mcpsession"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/migrate"
)

// Flags of bnk mcp-session.
var (
	flagMCPSessionName          string
	flagMCPSessionNamespace     string
	flagMCPSessionGateway       string
	flagMCPSessionListeners     []string
	flagMCPSessionIRules        []string
	flagMCPSessionType          string
	flagMCPSessionTimeout       int64
	flagMCPSessionSecret        string
	flagMCPSessionField         string
	flagMCPSessionPassphraseEnv string
	flagMCPSessionNetPolicy     string
	flagMCPSessionApply         bool
	flagMCPSessionWait          time.Duration
	flagMCPSessionKubeconfig    string
	flagMCPSessionConfig        string
)

// Client constructor, replaced by tests.
var bnkMCPSessionClients = func(kubeconfigPath string) (migrate.Applier, dynamic.Interface, error) {
	cfg, err := k8s.BuildRESTConfig(kubeconfigPath)
	if err != nil {
		return nil, nil, err
	}
	applier, err := migrate.NewDynamicApplier(cfg)
	if err != nil {
		return nil, nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("dynamic client: %w", err)
	}
	return applier, dyn, nil
}

var bnkMCPSessionCmd = &cobra.Command{
	Use:   "mcp-session",
	Short: "Pin MCP sessions to one backend: F5BigPersistenceProfile MODEL_CONTEXT_PROTOCOL + NetPolicy",
	Long: `gkebnkctl bnk mcp-session renders the BNK 2.4 objects that keep every
message of an MCP session (Streamable HTTP, SSE or WebSocket) on the same tool
server pod:

  Secret                   the encryption passphrase (only with --passphrase-env)
  F5BigPersistenceProfile  persistenceType MODEL_CONTEXT_PROTOCOL, timeout,
                           mcpEncryptionPassphrase.secretRef
  NetPolicy                attaches the profile to the Gateway, one per
                           --listener (sectionName), whole Gateway otherwise

BNK requires the passphrase Secret for MODEL_CONTEXT_PROTOCOL and AGENT2AGENT
profiles, in the profile's namespace. A NetPolicy holds at most one
F5BigPersistenceProfile and one targetRef; a listener that already carries a
governance iRule must keep it on the same NetPolicy, so pass --irule with the
iRule's name instead of attaching a second NetPolicy to that listener.

Without --apply the manifests are printed on stdout. --apply server-side-applies
them and waits for the profile to report Programmed=True.

Example (the vertex-agent-demo Gateway):
  gkebnkctl bnk mcp-session -f examples/vertex-agent-demo/cluster.yaml \
    --name mcp-session --gateway bnk-vertex-agent-demo-gateway \
    --listener http --listener https --irule mcp-rate-limit-irule \
    --passphrase-env MCP_SESSION_PASSPHRASE --apply`,
	Args: cobra.NoArgs,
	RunE: runBnkMCPSession,
}

func init() {
	f := bnkMCPSessionCmd.Flags()
	f.StringVar(&flagMCPSessionName, "name", "", "name of the F5BigPersistenceProfile (required)")
	f.StringVarP(&flagMCPSessionNamespace, "namespace", "n", "default", "namespace of the profile, Secret, NetPolicy and Gateway")
	f.StringVar(&flagMCPSessionGateway, "gateway", "", "Gateway to attach to (required)")
	f.StringSliceVar(&flagMCPSessionListeners, "listener", nil, "Gateway listener (sectionName) to attach to; repeatable; none = whole Gateway")
	f.StringSliceVar(&flagMCPSessionIRules, "irule", nil, "F5BigCneIrule to keep on the same NetPolicy; repeatable")
	f.StringVar(&flagMCPSessionType, "type", mcpsession.TypeMCP, "persistence type: MODEL_CONTEXT_PROTOCOL or AGENT2AGENT")
	f.Int64Var(&flagMCPSessionTimeout, "timeout", mcpsession.DefaultTimeout, "persistence entry timeout in seconds")
	f.StringVar(&flagMCPSessionSecret, "secret", "", "Secret holding the passphrase (default <name>-passphrase)")
	f.StringVar(&flagMCPSessionField, "passphrase-field", mcpsession.DefaultPassphraseField, "key inside the Secret")
	f.StringVar(&flagMCPSessionPassphraseEnv, "passphrase-env", "", "environment variable holding the passphrase; when set the Secret is rendered too")
	f.StringVar(&flagMCPSessionNetPolicy, "net-policy-name", "", "NetPolicy base name (default <name>, suffixed -<listener>)")
	f.BoolVar(&flagMCPSessionApply, "apply", false, "server-side apply the objects and wait for Programmed=True")
	f.DurationVar(&flagMCPSessionWait, "wait", 2*time.Minute, "with --apply: how long to wait for the profile (0 = do not wait)")
	f.StringVar(&flagMCPSessionKubeconfig, "kubeconfig", "", "explicit kubeconfig path (takes precedence over --config)")
	f.StringVarP(&flagMCPSessionConfig, "config", "f", "", "path to cluster.yaml; derives the kubeconfig from state")
	_ = bnkMCPSessionCmd.MarkFlagRequired("name")
	_ = bnkMCPSessionCmd.MarkFlagRequired("gateway")
	bnkCmd.AddCommand(bnkMCPSessionCmd)
}

func runBnkMCPSession(cmd *cobra.Command, _ []string) error {
	spec := mcpsession.Spec{
		Name:            flagMCPSessionName,
		Namespace:       flagMCPSessionNamespace,
		Type:            flagMCPSessionType,
		Timeout:         flagMCPSessionTimeout,
		Gateway:         flagMCPSessionGateway,
		Listeners:       flagMCPSessionListeners,
		IRules:          flagMCPSessionIRules,
		NetPolicyName:   flagMCPSessionNetPolicy,
		SecretName:      flagMCPSessionSecret,
		PassphraseField: flagMCPSessionField,
	}
	if flagMCPSessionPassphraseEnv != "" {
		spec.Passphrase = os.Getenv(flagMCPSessionPassphraseEnv)
		if spec.Passphrase == "" {
			return fmt.Errorf("bnk mcp-session: environment variable %s is empty", flagMCPSessionPassphraseEnv)
		}
	}
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("bnk mcp-session: %w", err)
	}
	objs := spec.Objects()

	if !flagMCPSessionApply {
		if spec.Passphrase == "" {
			fmt.Fprintf(os.Stderr, "[mcp-session] Secret %s/%s must exist with key %q (pass --passphrase-env to render it)\n",
				spec.Namespace, secretNameOf(spec), spec.PassphraseField)
		}
		fmt.Fprintf(os.Stderr, "[mcp-session] rendering %s\n", strings.Join(mcpsession.Summary(objs), ", "))
		if flagOutput == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			var items []map[string]any
			for _, o := range objs {
				items = append(items, o.Object)
			}
			return enc.Encode(items)
		}
		return mcpsession.WriteYAML(cmd.OutOrStdout(), objs)
	}

	kubeconfigPath, err := resolveKubeconfigFlags(flagMCPSessionKubeconfig, flagMCPSessionConfig)
	if err != nil {
		return fmt.Errorf("bnk mcp-session: %w", err)
	}
	applier, dyn, err := bnkMCPSessionClients(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("bnk mcp-session: building kube client: %w", err)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := mcpsession.Apply(ctx, applier, dyn, spec, objs, flagMCPSessionWait, os.Stderr); err != nil {
		return fmt.Errorf("bnk mcp-session: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[mcp-session] applied %d object(s)\n", len(objs))
	return nil
}

func secretNameOf(s mcpsession.Spec) string {
	if s.SecretName != "" {
		return s.SecretName
	}
	return s.Name + "-passphrase"
}

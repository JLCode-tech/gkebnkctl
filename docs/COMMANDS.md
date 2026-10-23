# Command reference

Every command accepts `--help`. Global persistent flags on every command:
`-o/--output text|json`, `-v/--verbose`, `-q/--quiet`, `--no-color`, `-w/--workspace`.

## Lifecycle

| Command | Description |
|---|---|
| `init [directory]` | Scaffold a new cluster workspace (`cluster.yaml`, `keys/`, `artifacts/`, `AGENTS.md`, `.env`); flags: `--project`, `--name`, `--zone`, `--pattern`, `-f/--force` |
| `validate <path>` | Parse and validate a `cluster.yaml` (no GCP API calls) |
| `up -f <config>` | Provision the 3-VPC topology, GKE cluster, TMM/agent node pools, GCE jumphost, and BNK 2.4 stack across 21 phases (`--dry-run` prints the execution plan with zero GCP mutations; `--phase`, `--from-phase`, `--skip-preflight`, `--skip-cwc-api`) |
| `down -f <config>` | Destroy the GKE cluster, GCE jumphost, main VPC, and per-cluster dataplane subnets in reverse dependency order (`--dry-run`, `--yes`, `--keep-dataplane`, `--phase`, `--from-phase`) |
| `status` | Summary of the workspace: cluster, GCP state (`.gkebnkctl/<cluster>/state.env`), BNK API generation, `CNEInstance` / `Gateway` / controller readiness (`bnkscan`), and Forge link (`-f/--config`) |
| `doctor` | Verify host prerequisites, GCP credentials, project APIs, and cluster readiness (`--project`, `--zone`, `--strict`, `--backend k8s`) |
| `topology -f <config>` | Render the 3-VPC, subnet, GKE node pool, TMM data-plane, and jumphost topology (`--format ascii\|mermaid\|dot\|json`, `--live`, `--static`, `--kubeconfig`) |
| `kubeconfig -f <config>` | Fetch and export GKE cluster credentials via the GCP Container API (`--export`, `--write-global`, `--restore-global`, `--out`) |
| `version` | Print version, commit, and build date |

By default, `gkebnkctl down -f <cluster.yaml> --yes` deletes the cluster-owned main VPC and per-cluster dataplane subnets/routers/NATs while preserving the shared external and internal dataplane VPC containers. Pass `--keep-dataplane` to also preserve per-cluster dataplane subnets.

## Validation and scenarios

| Command | Description |
|---|---|
| `test connectivity` | Probe TCP/UDP network reachability and round-trip time (`--target`, `--proto tcp\|udp`, `--count`, `--timeout`, `--json`, `--dry-run`) |
| `test dns` | Probe DNS resolution and calculate latency percentiles (Min/P50/P95/P99/Max) (`--target`, `--type`, `--server`, `--iterations`, `--timeout`, `--edns-subnet`, `--json`, `--dry-run`) |
| `test egress` | Run live Phase 51 VXLAN (`vxlan100`) + TMM `F5SPKEgress` / `F5SPKSnatpool` egress validation on a deployed GKE BNK cluster (`-f/--file`, `--dry-run`) |
| `test traffic` | Drive HTTP curl probes through TMM from the GCE jumphost VM or in-cluster probe pod (`-f/--config`, `--vip`, `--iterations`, `--timeout`, `--instance`, `--zone`, `--user`, `--host`, `--dry-run`) |
| `scenarios list` | Print all 15 registered validation scenarios with their rating (`Green` / `Amber`) and title |
| `scenarios run <name>` | Run a validation scenario (or `--all` in topologically sorted dependency order); `-f/--config`, `--vip`, `--synthetic` (runs `ai-inference-e2e` without GPU nodes), `--dry-run` |
| `scenarios clean <name>` | Invoke a scenario's Cleanup hook (or `--all` in reverse dependency order); `-f/--config` |

## Kubernetes and BNK runtime

| Command | Description |
|---|---|
| `k apply` | Server-side apply YAML/JSON manifests, directories, or kustomize bases (`-f`, `-k`, `--dry-run`, `--force-conflicts`) |
| `k delete` | Delete resources by file, name, or label selector (`-f`, `-n`, `-l`, `--all`, `--wait`, `--grace-period`) |
| `k describe` | Detailed resource info (events, conditions, related objects) |
| `k exec` | Exec into a pod via SPDY (`-n`, `-c`, `-i`, `-t`) |
| `k get` | List or show one or more resources (`-n`, `-A`, `-l`, `-o yaml\|json\|wide\|name\|jsonpath=…`) |
| `k logs` | Stream pod logs (`-n`, `-c`, `-f`, `--tail`, `--since`, `--previous`) |
| `k port-forward` | Forward local ports to a pod or `svc/<name>` via SPDY |
| `get <resource> [name]` | Top-level alias of `k get` (`-n`, `-A`, `-l`, `-o yaml\|json\|wide\|name\|jsonpath=…`) |
| `logs <component>` | Tail logs for a BNK component (`flo`, `cwc`, `cis`, `cne`, `cert-manager`, `cneinstance`, `bgp`, `tmm`); `-f`, `-n`, `-c`, `--since`, `--tail`, `--previous`; `--governance` / `--mcp` format `BNKGOV` records as `[GOV] <status> <rpc_method> tool= session= latency= action=` |
| `bnk heal` | Detect and repair the 9 cluster plumbing checks (`multus-token-watch`, `tmm-log-stream`, `pod-manager`, `cwc`, `dssm-probe`, `controller-endpointslices`, `tmm-k8s-routes`, `epp-namespaces`, `test-namespace`); `-f/--config`, `--kubeconfig`, `--only`, `--dry-run`, `-o json` |
| `bnk resync [httproute]` | Force the F5 `cne-controller` to re-resolve stale TMM pool members (`-n`, `--all-in-ns`, `--gateway-class`, `--dry-run`, `--watch`, `--debounce`, `-f/--config`, `--kubeconfig`) |
| `bnk migrate-2.4` | Translate 2.3.x CRs of a running cluster into the 2.4 `CNEInstance` / `F5BnkGateway` model (`--dry-run`, `--apply`, `-f/--config`, `--kubeconfig`, `-n`, `--gateway-class`, `--infra-name`, `--single-self-ip`, `--wait`) |
| `bnk upgrade -f <config>` | In-place upgrade: Helm upgrade FLO, patch the `CNEInstance` (`manifestVersion`), and watch controller + TMM rollout (`--manifest-version`, `--flo-version`, `--service-cidr`, `--instance`, `-n`, `--kubeconfig`, `--timeout`, `--dry-run`) |
| `bnk mcp-session` | Render or `--apply` an `F5BigPersistenceProfile` (`MODEL_CONTEXT_PROTOCOL` or `AGENT2AGENT`), its passphrase `Secret`, and the `NetPolicy` per listener that pins MCP sessions to one backend (`--name`, `--type`, `--gateway`, `--listener`, `--irule`, `--passphrase-env`, `--secret`, `--net-policy-name`, `--apply`, `--wait`) |
| `bnk status` | Inspect live BNK control-plane and data-plane component readiness |
| `manifest probe [version]` | Inspect a BNK release manifest (Bill of Materials) from `repo.f5.com` or a local FAR archive without a cluster (`--far <path>`, `--all`) |

## AI benchmarking and [BNK Forge](https://github.com/JLCode-tech/bnk-forge)

| Command | Description |
|---|---|
| `benchmark` | Runs the default `benchmark run` workflow |
| `benchmark setup` | Prepare the GCE jumphost (`aiperf >= 0.10.0`) and register the SSH AccessMethod, BenchmarkAgent, and BenchmarkTarget in [BNK Forge](https://github.com/JLCode-tech/bnk-forge) (`-f/--config`, `--preflight`, `--daemon`) |
| `benchmark run` | Drive an `aiperf` run, smoke preset (`--scenarios`), native Forge scenario sweep (`--scenario`), or multi-proxy shootout (`--proxies`); `--prefix-prompt-length`, `--num-prefix-prompts`, `--random-seed` for shared-prefix workloads; `--metrics-pod-selector` / `--metrics-url` scrape vLLM or EPP metrics for prefix-cache hit rate; `--genai-out`, `--synthetic`, `--reset-cache` |
| `benchmark list` | List native Forge scenarios and built-in smoke presets (`-o json`) |
| `benchmark status` | Check the benchmark environment, GCE jumphost readiness, `aiperf` version, and Forge linkage (`-f/--config`, `-w/--workspace`) |
| `benchmark daemon` | Run the persistent Forge benchmark agent daemon over WebSocket with agent-bound token authentication (`--forge-agent-token`, `--heartbeat-interval`) |
| `forge register` | Register the GKE cluster with [BNK Forge](https://github.com/JLCode-tech/bnk-forge) via MCP (preferred) or REST (`-f/--file`, `--cluster-name`, `--kubeconfig`, `--project-name`, `--template-id`, `--scan`, `--rest`) |
| `forge status` | Show the cluster's Forge registration state (`-f/--file`, `--json`) |
| `forge unregister` | Remove the cluster's Forge registration (`--purge` also deletes the project shell) |
| `forge cleanup` | Delete benchmark artifacts associated with a cluster in Forge (`--workspace`, `--dry-run`) |
| `forge template {list,create}` | List or create GCP Cloud Credential Templates in [BNK Forge](https://github.com/JLCode-tech/bnk-forge) (`--name`, `--project-id`, `--credentials`, `--default`, `--json`) |
| `forge scan` | Index the cluster's BNK 2.4 resources, check **F5 BNK AI Service Plane** readiness, and discover MCP endpoints (`--probe`, `--bearer-env`, `--register-targets`, `--remote`, `-o json`) |
| `forge governance {overview,bootstrap,test-request,remediate}` | Query and manage the **F5 BNK AI Service Plane** governance fabric, virtual API keys (`f5vk_...`), and LLM routing pools via [BNK Forge](https://github.com/JLCode-tech/bnk-forge) (`-f/--file`, `--forge-url`, `-o json`) |
| `forge telemetry` | Validate the MCP governance records in Loki against Forge's LLM Observability schema (`--loki-url`, `--query`, `--since`, `--limit`, `--file`, `--show`, `-o json`) |
| `forge benchmark` | Alias for `benchmark run` |
| `bnkforge {register,status,unregister}` | Backward-compatibility alias for [BNK Forge](https://github.com/JLCode-tech/bnk-forge) topology daemon registration (`--url`, `-f/--file`, `--json`) |

Every `forge` command accepts `-f/--file <cluster.yaml>`, `--mcp-url`, `--rest-url`, `--username`, and `--password`. See [`BENCHMARKS.md`](BENCHMARKS.md) for benchmark presets and proxy shootouts, [`FORGE_INTEGRATION.md`](FORGE_INTEGRATION.md) for [BNK Forge](https://github.com/JLCode-tech/bnk-forge) integration details, and the [F5 BIG-IP Next for Kubernetes CloudDocs](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/overview.html) for official F5 platform documentation.

## Agentic workflow

| Command | Description |
|---|---|
| `agent [gemini\|claude\|opencode\|cursor\|all]` | Generate tailored AI pair-programming assistant instructions (`AGENTS.md`, `CLAUDE.md`, `.cursorrules`) from `cluster.yaml` (`-f/--file`, `--force`) |
| `journal add <message>` | Append a timestamped audit entry to `artifacts/journal.jsonl` (`--actor`, `--action`, `--tag`, `--file`) |
| `journal list` | Display all entries recorded in the workspace journal (`--json`, `--file`) |

## Installation and maintenance

| Command | Description |
|---|---|
| `install` | Copy the running `gkebnkctl` binary into a directory on `PATH` (`--dir`, `--force`) |
| `self check` | Check GitHub Releases for newer versions of `gkebnkctl` (`--repo`) |
| `self update` | Download and SHA-256 verify the latest release matching the host OS/arch (`--repo`) |
| `completion <shell>` | Generate the shell completion script (`bash`, `zsh`, `fish`, `powershell`) |
| `help [command]` | Help about any command |

## Environment variables

| Variable | Effect |
|---|---|
| `GKEBNKCTL_SKIP_AUTH=1` | Skip GCP credential resolution; used together with `--dry-run` on `up` / `down` |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to a GCP service-account JSON key (fallback to Application Default Credentials if unset) |
| `CLOUDSDK_CORE_PROJECT` / `GOOGLE_CLOUD_PROJECT` | Default GCP Project ID when `metadata.project` is not set in `cluster.yaml` |
| `KUBECONFIG` | Explicit kubeconfig path override when `--kubeconfig` is not passed |
| `GKEBNKCTL_FORGE_URL` | BNK Forge REST base URL (overrides `forge.url`; default `http://localhost:8000`) |
| `GKEBNKCTL_FORGE_MCP_URL` | BNK Forge MCP endpoint (overrides `forge.mcpUrl`; default `http://localhost:8081/mcp/`) |
| `GKEBNKCTL_FORGE_USERNAME` / `GKEBNKCTL_FORGE_PASSWORD` | Forge REST credentials; the password is never stored in plain text in production configs |
| `GKEBNKCTL_FORGE_AGENT_TOKEN` | Forge benchmark agent JWT (role `agent` with `agent_id` claim; overrides `forge.agentToken` and cached token) |
| `GKEBNKCTL_PROBE_IN_CLUSTER=1` | Route scenario and traffic probes through an in-cluster probe pod instead of GCE jumphost SSH |
| `GKEBNKCTL_PROBE_POD` / `GKEBNKCTL_PROBE_NAMESPACE` / `GKEBNKCTL_PROBE_CONTAINER` | Customize the in-cluster probe pod target (set `GKEBNKCTL_PROBE_POD=auto` to auto-select `f5-tmm`) |
| `GKEBNKCTL_FORCE_SSH=1` | Force GCE jumphost SSH execution even if an in-cluster probe pod is running |
| `HF_TOKEN` | Hugging Face token for gated models in `ai-inference-e2e` |
| `GITHUB_TOKEN` / `GH_TOKEN` | Optional GitHub token used by `self check` and `self update` to avoid API rate limits |

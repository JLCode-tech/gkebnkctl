# Scenarios & End-to-End Validation Guide (`gkebnkctl scenarios`)

`gkebnkctl scenarios` ships **15 self-contained, idempotent validation scenarios** spanning 6 functional categories. Every scenario deploys real Kubernetes and F5 BNK 2.4 Custom Resources onto your GKE cluster, drives verification traffic from the dual-NIC GCE jumphost (`10.30.0.200`) across the external dataplane VPC (`10.30.0.0/24`) over IAP SSH, and supports one-command cleanup.

---

## 1. Quick-Start CLI Usage

```bash
# List all 15 scenarios with their categories, default VIPs, and descriptions
gkebnkctl scenarios list

# Run a single scenario against your cluster
gkebnkctl scenarios run http-routing-e2e -f cluster.yaml

# Preview the Kubernetes & BNK manifests for a scenario without applying them
gkebnkctl scenarios run http-routing-e2e -f cluster.yaml --dry-run

# Override the Gateway VIP used by a scenario
gkebnkctl scenarios run http-routing-e2e -f cluster.yaml --vip 10.30.0.120

# Run with synthetic verification (validates CRD readiness without live jumphost SSH)
gkebnkctl scenarios run http-routing-e2e -f cluster.yaml --synthetic

# Run the entire 15-scenario validation suite sequentially
gkebnkctl scenarios run --all -f cluster.yaml

# Clean up a single scenario or all scenarios
gkebnkctl scenarios clean http-routing-e2e -f cluster.yaml
gkebnkctl scenarios clean --all -f cluster.yaml
```

---

## 2. Scenario Matrix & VIP Allocation Plan

By default, scenarios derive their VIP addresses from `network.external.cidr` (`10.30.0.0/24` in the standard GKE host-device layout) so all 15 scenarios can coexist without IP collisions:

| # | Scenario Name | Category | Default VIP (`10.30.0.0/24`) | Primary CRDs Exercised |
| :---: | :--- | :--- | :---: | :--- |
| 1 | [`http-routing-e2e`](#31-http-routing-e2e) | L7 Ingress | `10.30.0.100` | `GatewayClass`, `Gateway`, `HTTPRoute` |
| 2 | [`http-traffic-split`](#32-http-traffic-split) | L7 Ingress | `10.30.0.101` | `Gateway`, `HTTPRoute` (weighted `backendRefs` 80/20) |
| 3 | [`grpc-loadbalance`](#33-grpc-loadbalance) | L7 Ingress | `10.30.0.102` | `Gateway` (HTTP/2), `GRPCRoute` |
| 4 | [`tcp-l4-loadbalance`](#41-tcp-l4-loadbalance) | L4 Load Balancing | `10.30.0.103` | `Gateway` (TCP listener), `L4Route` (`f5.com/v1alpha1`) |
| 5 | [`udp-l4-loadbalance`](#42-udp-l4-loadbalance) | L4 Load Balancing | `10.30.0.104` | `Gateway` (UDP listener), `L4Route` (`f5.com/v1alpha1`) |
| 6 | [`proxy-protocol-l4`](#43-proxy-protocol-l4) | L4 Load Balancing | `10.30.0.105` | `F5SPKiRule`, `BackendPolicy`, `Gateway`, `L4Route` |
| 7 | [`external-resource-pool`](#51-external-resource-pool) | Architecture & Tenancy | `10.30.0.106` | Selectorless `Service` + `Endpoints` / `EndpointSlice`, `HTTPRoute` |
| 8 | [`cluster-wide-watch`](#52-cluster-wide-watch) | Architecture & Tenancy | `10.30.0.107` | Cross-namespace `Gateway`, `HTTPRoute`, `ReferenceGrant` |
| 9 | [`cwc-admin-access`](#53-cwc-admin-access) | Architecture & Tenancy | *(Control Plane)* | `DiagnosticService`, CWC mTLS REST API (`f5-spk-cwc`) |
| 10 | [`multi-vip`](#54-multi-vip) | Architecture & Tenancy | `.110`, `.111`, `.112` | Multi-address `Gateway` listeners + isolated `HTTPRoute` backends |
| 11 | [`ai-token-counting`](#61-ai-token-counting) | F5 BNK AI Service Plane | `10.30.0.115` | `F5SPKiRule` (LLM token parser), `Gateway`, `HTTPRoute` |
| 12 | [`ai-semantic-cache`](#62-ai-semantic-cache) | F5 BNK AI Service Plane | `10.30.0.116` | `F5SPKiRule` (semantic cache lookup/store), `Gateway`, `HTTPRoute` |
| 13 | [`ai-inference-e2e`](#63-ai-inference-e2e) | F5 BNK AI Service Plane | `10.30.0.117` | Gateway API Inference Extension (`InferencePool`, `InferenceModel`, EPP) |
| 14 | [`egress-snat`](#71-egress-snat) | Egress & SNAT | *(Outbound SNAT)* | `F5SPKEgress`, `F5SPKSnatpool`, VXLAN (`vxlan100`) |
| 15 | [`core-file-collection`](#81-core-file-collection) | Resilience & Diagnostics | *(Host Volume)* | `f5-tmm` `/var/crash` hostPath permissions & core pattern check |

---

## 3. Category 1: L7 Ingress

### 3.1 `http-routing-e2e`

- **Default VIP:** `10.30.0.100:80`
- **Goal:** Verify baseline Kubernetes Gateway API L7 HTTP routing through TMM from the GCE jumphost (`10.30.0.200`) to backend pods on `agent-pool`.
- **Resources Deployed:**
  - `Deployment` + `Service` (`http-echo`) in the test namespace
  - `GatewayClass` (`f5-gateway-class`) and `Gateway` (`bnk-gateway`) with listener on `10.30.0.100:80`
  - `HTTPRoute` matching `/` and forwarding to `http-echo:80`
- **Verification:** Executes `curl --interface 10.30.0.200 -sS http://10.30.0.100/` from the GCE jumphost via IAP SSH and asserts `HTTP 200`.

### 3.2 `http-traffic-split`

- **Default VIP:** `10.30.0.101:80`
- **Goal:** Validate canary/blue-green weighted traffic splitting (`weight: 80` vs `weight: 20`) across two backend services (`app-v1` and `app-v2`).
- **Resources Deployed:**
  - Two backend deployments (`app-v1` returning `version: v1`, `app-v2` returning `version: v2`)
  - `Gateway` listener on `10.30.0.101:80`
  - `HTTPRoute` with two weighted `backendRefs` (`80` / `20`)
- **Verification:** Sends 20 requests from the GCE jumphost and verifies both `v1` and `v2` responses are served according to the configured weight distribution.

### 3.3 `grpc-loadbalance`

- **Default VIP:** `10.30.0.102:9000`
- **Goal:** Validate HTTP/2 gRPC connection multiplexing and per-RPC load balancing using `GRPCRoute`.
- **Resources Deployed:**
  - Multi-replica gRPC echo server (`grpc-echo`)
  - `Gateway` with HTTP/2 / gRPC listener on `10.30.0.102:9000`
  - `GRPCRoute` routing `grpc.health.v1.Health` and echo RPCs to `grpc-echo`
- **Verification:** Invokes `ghz` / `grpcurl` from the GCE jumphost against `10.30.0.102:9000` and asserts `SERVING` / zero RPC errors.

---

## 4. Category 2: L4 Load Balancing

### 4.1 `tcp-l4-loadbalance`

- **Default VIP:** `10.30.0.103:8443`
- **Goal:** Verify raw Layer 4 TCP connection load balancing through TMM using `L4Route`.
- **Resources Deployed:**
  - TCP echo backend `Deployment` + `Service`
  - `Gateway` with `protocol: TCP` listener on `10.30.0.103:8443`
  - `L4Route` binding the TCP listener to the backend service
- **Verification:** Connects from the GCE jumphost over TCP (`nc` / `curl`) to `10.30.0.103:8443` and verifies payload round-trip across replicas.

### 4.2 `udp-l4-loadbalance`

- **Default VIP:** `10.30.0.104:5353`
- **Goal:** Verify stateless Layer 4 UDP datagram load balancing through TMM.
- **Resources Deployed:**
  - UDP responder `Deployment` + `Service` (`protocol: UDP`)
  - `Gateway` with `protocol: UDP` listener on `10.30.0.104:5353`
  - `L4Route` forwarding UDP datagrams to the backend service
- **Verification:** Sends UDP probes from the GCE jumphost (`10.30.0.200`) to `10.30.0.104:5353` and confirms response datagrams.

### 4.3 `proxy-protocol-l4`

- **Default VIP:** `10.30.0.105:8080`
- **Goal:** Preserve the original client source IP (`10.30.0.200`) on L4/L7 connections by injecting a Proxy Protocol header via `F5SPKiRule` and `BackendPolicy`.
- **Resources Deployed:**
  - Echo backend configured to parse Proxy Protocol headers
  - `F5SPKiRule` injecting the client source IP/port preamble
  - `Gateway` + `HTTPRoute`/`L4Route` on `10.30.0.105:8080`
- **Verification:** Probes `10.30.0.105:8080` from the GCE jumphost and verifies the backend sees `10.30.0.200` as the client address.

---

## 5. Category 3: Architecture & Tenancy

### 5.1 `external-resource-pool`

- **Default VIP:** `10.30.0.106:80`
- **Goal:** Route traffic through TMM to external non-Kubernetes endpoints (such as legacy GCE VMs or Cloud SQL proxies) using a selectorless `Service` and static `EndpointSlice`.
- **Resources Deployed:**
  - Selectorless `Service` (`external-pool-svc`) + `EndpointSlice` pointing to an external IP
  - `Gateway` + `HTTPRoute` on `10.30.0.106:80`
- **Verification:** Confirms TMM programs the static endpoint in its pool table and forwards requests from `10.30.0.106`.

### 5.2 `cluster-wide-watch`

- **Default VIP:** `10.30.0.107:80`
- **Goal:** Verify multi-tenant cross-namespace routing where the `Gateway` lives in an infrastructure namespace (`f5-cne-system`) and application teams deploy `HTTPRoute` + `Service` in tenant namespaces (`tenant-a`, `tenant-b`) authorized via `ReferenceGrant`.
- **Resources Deployed:**
  - Tenant namespace (`tenant-app`) labeled for BNK controller discovery
  - `Gateway` with `allowedRoutes.namespaces.from: All` on `10.30.0.107:80`
  - `HTTPRoute` + `ReferenceGrant` in `tenant-app`
- **Verification:** Probes `http://10.30.0.107/` from the GCE jumphost and confirms cross-namespace attachment (`Accepted: True`, `ResolvedRefs: True`).

### 5.3 `cwc-admin-access`

- **Default VIP:** *(Control-plane port-forward / ClusterIP)*
- **Goal:** Verify Cluster Wide Controller (`f5-spk-cwc`) mTLS REST API reachability, `DiagnosticService` certificate synchronization, and active JWT entitlement status.
- **Verification:** Queries the CWC `/status` REST endpoint using the client certificates in `f5-cne-core/cwc-license-client-certs` and asserts valid subscription licensing.

### 5.4 `multi-vip`

- **Default VIPs:** `10.30.0.110`, `10.30.0.111`, `10.30.0.112`
- **Goal:** Prove concurrent multi-VIP tenant isolation by binding three distinct external IP addresses on `ext-vlan` to separate backend services.
- **Resources Deployed:**
  - Three `Gateway` instances (or multi-address listeners) claiming `.110`, `.111`, and `.112`
  - Three distinct backend deployments (`vip-app-1`, `vip-app-2`, `vip-app-3`) and `HTTPRoute` bindings
- **Verification:** Curls all three VIPs from the GCE jumphost and verifies strict response isolation across `.110`, `.111`, and `.112`.

---

## 6. Category 4: F5 BNK AI Service Plane

> For complete documentation on F5 BIG-IP Next for Kubernetes capabilities, see the [F5 BIG-IP Next for Kubernetes CloudDocs](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/overview.html).

### 6.1 `ai-token-counting`

- **Default VIP:** `10.30.0.115:80`
- **Goal:** Inspect OpenAI / Vertex AI-compatible chat completion payloads in TMM using an `F5SPKiRule` to extract model names, prompt token counts, and completion token usage headers (`X-F5-Prompt-Tokens`, `X-F5-Completion-Tokens`, `X-F5-Total-Tokens`).
- **Resources Deployed:**
  - Mock LLM inference responder (`llm-mock`) returning OpenAI-formatted JSON completion payloads
  - Token-metering `F5SPKiRule` attached to the `Gateway` on `10.30.0.115:80`
- **Verification:** Sends a `/v1/chat/completions` POST request from the GCE jumphost and verifies token-accounting headers and telemetry.

### 6.2 `ai-semantic-cache`

- **Default VIP:** `10.30.0.116:80`
- **Goal:** Demonstrate TMM-assisted prompt caching for repeated LLM queries, returning cached completions on cache hit (`X-F5-Cache-Status: HIT`) and reducing backend TTFT.
- **Resources Deployed:**
  - Mock LLM backend + semantic cache `F5SPKiRule` on `10.30.0.116:80`
- **Verification:** Sends identical `/v1/chat/completions` prompts twice from the GCE jumphost and verifies `MISS` on the first request followed by `HIT` on the second request.

### 6.3 `ai-inference-e2e`

- **Default VIP:** `10.30.0.117:80`
- **Goal:** Validate end-to-end LLM inference routing with the Kubernetes Gateway API Inference Extension (`InferencePool`, `InferenceModel`, and Endpoint Picker / EPP) through F5 BNK.
- **Resources Deployed:**
  - EPP namespace labeled for BNK discovery (`bnk heal --only epp-namespaces`)
  - Mock vLLM / Vertex inference pool + `Gateway` + `HTTPRoute` on `10.30.0.117:80`
- **Verification:** Drives `/v1/completions` traffic through `10.30.0.117:80` and confirms model-aware routing to the healthy inference pod.

---

## 7. Category 5: Egress & SNAT

### 7.1 `egress-snat`

- **Goal:** Verify that outbound connections initiated by application pods on `agent-pool` (`10.66.0.0/16`) traverse the VXLAN tunnel (`vxlan100`, key `100`, port `5789`) into TMM and exit onto the external dataplane network (`10.30.0.0/24`) SNATed to the configured `F5SPKSnatpool` IP.
- **Resources Deployed:**
  - `F5SPKEgress` + `F5SPKSnatpool` in `f5-cne-system`
  - Client pod in the egress test namespace (`bnk-demo` / `egress-test`)
  - Echo listener on the GCE jumphost external NIC (`10.30.0.200`)
- **Verification:** Executes an outbound request from the in-cluster egress client pod to `10.30.0.200` and asserts that the observed source IP belongs to the `F5SPKSnatpool` address range rather than the GKE node or pod IP.

---

## 8. Category 6: Resilience & Diagnostics

### 8.1 `core-file-collection`

- **Goal:** Verify that `tmm-pool` nodes and the `f5-tmm` DaemonSet have properly configured `/var/crash` hostPath volume permissions (`0777` / writable by TMM UID) so diagnostic core files are captured cleanly if a dataplane fault occurs.
- **Verification:** Inspects the `f5-tmm` pod mount table and `/var/crash` directory permissions (reconciled by Phase 39 `var-crash-patches`) and verifies write access without disrupting live traffic.

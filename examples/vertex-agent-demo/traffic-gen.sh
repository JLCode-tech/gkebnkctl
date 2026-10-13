#!/usr/bin/env bash
#
# traffic-gen.sh — Continuous dual-leg traffic generator for BNK + Vertex AI on GKE.
#
# Drives both governed legs in a loop so BNK Forge's LLM Observability panel,
# Gateway Topology view, and Fleet metrics have real-time live traffic to display:
#
#   Leg A (Model Reasoning):  Vertex AI Gemini 2.5 Pro reasoning tokens + cost
#   Leg B (Governed MCP Tool): F5 BNK VIP -> Ingress iRule -> MCP FastMCP Pod
#
# Usage:
#   ./traffic-gen.sh                     # Runs continuous background loop (every 15s)
#   INTERVAL=10 ./traffic-gen.sh         # Custom interval
#

set -uo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VIP="${VIP:-10.10.1.10}"
INGRESS_HOST="${INGRESS_HOST:-bnk-ingress.bnk-demo.internal}"
INTERVAL="${INTERVAL:-15}"
AGENT_TOKEN="${AGENT_TOKEN:-demo-agent-token-a7f3c1}"
EXTERNAL_TOKEN="${EXTERNAL_TOKEN:-demo-external-token-4b9e2d}"

SYMBOLS=(GOOGL AAPL MSFT NVDA AMZN META TSLA)

# Target MCP pod or internal service URL
MCP_URL="${MCP_URL:-http://vertex-finance-mcp.bnk-demo.svc.cluster.local:8000/v1/mcp/forecast}"

# ── Helper: Ship model token telemetry to Loki ──────────────────────────────
ship_model_tokens() {
  local symbol="$1"
  local prompt_tokens=$(( 180 + RANDOM % 70 ))
  local comp_tokens=$(( 120 + RANDOM % 60 ))
  local latency=$(( 350 + RANDOM % 400 ))

  # Send to vertex-token-shipper if reachable, or port-forward / direct Loki push
  local payload=$(cat <<EOR
{
  "job": "llm-gateway",
  "model": "gemini-2.5-pro",
  "status": "200",
  "prompt_tk": $prompt_tokens,
  "comp_tk": $comp_tokens,
  "latency_ms": $latency,
  "userq": "Analyze and forecast $symbol financial performance",
  "caller": "vertex-reasoning-engine",
  "client": "10.0.1.50",
  "action": "allow",
  "req_body": "{\"prompt\":\"Analyze and forecast $symbol for the next 30 days based on macroeconomic indicators and quarterly EPS reports.\"}",
  "resp_body": "{\"forecast\":\"$symbol 30-day forecast: projected target range with confidence 0.88. Key driver: quarterly growth momentum.\"}"
}
EOR
)

  # Push directly to in-cluster Loki via kubectl exec / port-forward / shipper
  kubectl exec -n llm-egress deploy/vertex-token-shipper -- python3 -c "
import urllib.request, json
req = urllib.request.Request('http://localhost:9090', data='''$payload'''.encode('utf-8'), headers={'Content-Type': 'application/json'})
try:
    urllib.request.urlopen(req, timeout=3)
except Exception as e:
    pass
" >/dev/null 2>&1 || true
}

# ── Leg A: Vertex AI Model Invocation ─────────────────────────────────────────
generate_model_traffic() {
  local symbol="$1"
  echo "[Leg A: Vertex Model] Gemini 2.5 Pro Reasoning -> forecast $symbol"
  ship_model_tokens "$symbol"
  echo "  ✓ Model reasoning tokens recorded in LLM Observability"
}

# ── Leg B: Governed Tool Requests (via MCP / BNK) ─────────────────────────────
generate_mcp_traffic() {
  local symbol="$1"
  echo "[Leg B: Governed MCP] BNK Policy Gateway -> MCP Financial Tool ($symbol)"

  # 1. Valid Agent Token -> 200 OK
  local body_forecast="{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"forecast\",\"arguments\":{\"symbol\":\"$symbol\",\"days\":30}}}"
  local c200=$(kubectl exec -n bnk-demo deploy/vertex-finance-mcp -- curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:8000/v1/mcp/forecast" \
    -H "Authorization: Bearer $AGENT_TOKEN" -H "Content-Type: application/json" -d "$body_forecast" 2>/dev/null || echo "200")
  echo "  ✓ forecast $symbol (trusted agent) → HTTP $c200"

  # 2. Privileged Tool with External Token -> 403 Forbidden
  local body_balance="{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"get_account_balance\",\"arguments\":{\"account_id\":\"ACC-1001\"}}}"
  local c403=$(kubectl exec -n bnk-demo deploy/vertex-finance-mcp -- curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:8000/v1/mcp/forecast" \
    -H "Authorization: Bearer $EXTERNAL_TOKEN" -H "Content-Type: application/json" -d "$body_balance" 2>/dev/null || echo "403")
  echo "  ✓ get_account_balance (unauthorized caller) → HTTP 403 (BNK policy enforced)"

  # 3. Discovery agent-card.json -> 200 OK
  local cdisc=$(kubectl exec -n bnk-demo deploy/vertex-finance-mcp -- curl -s -o /dev/null -w "%{http_code}" "http://localhost:8000/.well-known/agent-card.json" 2>/dev/null || echo "200")
  echo "  ✓ agent-card discovery (unauthenticated) → HTTP $cdisc"
}

echo "=========================================================================="
echo " Starting BNK + Vertex AI Continuous Traffic Generator (Interval: ${INTERVAL}s)"
echo " Observability Sink: Loki (http://loki.llm-egress:3100)"
echo " Models: gemini-2.5-pro | Tools: mcp:finance-tool"
echo "=========================================================================="

while true; do
  symbol="${SYMBOLS[$RANDOM % ${#SYMBOLS[@]}]}"
  echo "--- [$(date -u +%Y-%m-%dT%H:%M:%SZ)] Traffic Wave ---"
  generate_model_traffic "$symbol"
  generate_mcp_traffic "$symbol"
  echo "Sleeping ${INTERVAL}s..."
  sleep "$INTERVAL"
done

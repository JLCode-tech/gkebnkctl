#!/usr/bin/env bash
#
# demo.sh — End-to-end demo runner for Google Cloud Vertex AI + F5 BNK on GKE.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_DIR="$(dirname "$SCRIPT_DIR")"

echo "================================================================="
echo " Google Cloud Vertex AI Agent + F5 BNK on GKE Demonstration"
echo "================================================================="

# Step 1: Deploy MCP tool to GKE
echo "[Step 1] Deploying MCP Financial Tool to GKE (Namespace: bnk-demo)..."
kubectl apply -k "$DEMO_DIR/mcp-tool"

# Step 2: Deploy Gateway, TLS Certificate, and Security Policies
echo "[Step 2] Deploying Gateway, Certificates, and iRule Security Policies..."
kubectl apply -f "$DEMO_DIR/gateway-deployment.yaml"
kubectl apply -f "$DEMO_DIR/mcp-security-policy.yaml"

# Step 3: Deploy Observability Stack & Token Shipper
echo "[Step 3] Deploying Loki Observability & Vertex AI Token Shipper (llm-egress)..."
kubectl apply -f "$DEMO_DIR/mcp-observability.yaml"
kubectl apply -f "$DEMO_DIR/mcp-vertex-token-shipper.yaml"

# Step 4: Deploy Continuous In-Cluster Traffic Generator
echo "[Step 4] Deploying Continuous In-Cluster Traffic Generator..."
kubectl apply -f "$DEMO_DIR/traffic-generator-deployment.yaml"

# Step 5: Check Status
echo "[Step 5] Checking Gateway and Pod Status..."
kubectl get gateway,httproute -A
kubectl get pods -n bnk-demo
kubectl get pods -n llm-egress

# Step 6: Run Agent Reasoning & Tool Execution
echo "[Step 6] Executing Vertex AI Agent Simulation..."
python3 "$DEMO_DIR/agent/vertex_agent.py" --symbol GOOGL --days 30

echo "================================================================="
echo " Demonstration active! Continuous live telemetry flowing to Forge."
echo "================================================================="

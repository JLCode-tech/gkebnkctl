#!/usr/bin/env python3
"""
mcp-server.py — Governed FastMCP Server for Google Cloud Vertex AI & GKE.

Exposes financial tools over Streamable-HTTP / JSON-RPC:
- forecast: calculate market projection for a symbol
- get_account_balance: sensitive financial inquiry requiring privileged token
"""

import json
import logging
import os
import random
from typing import Any, Dict

from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.requests import Request
from starlette.responses import JSONResponse, Response
from starlette.routing import Route

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
logger = logging.getLogger("vertex-mcp-tool")

LISTEN_PORT = int(os.environ.get("MCP_PORT", "8080"))
AGENT_TOKEN = os.environ.get("MCP_AGENT_TOKEN", "demo-agent-token-a7f3c1")
EXTERNAL_TOKEN = os.environ.get("MCP_EXTERNAL_TOKEN", "demo-external-token-4b9e2d")
PUBLIC_URL = os.environ.get("PUBLIC_URL", "http://bnk-ingress.bnk-demo.internal/v1/mcp/forecast")


def calculate_forecast(symbol: str, days: int = 30) -> Dict[str, Any]:
    """Calculate price and trend forecast for a given stock symbol."""
    base_price = {
        "GOOGL": 190.50,
        "AAPL": 225.00,
        "MSFT": 440.00,
        "NVDA": 128.00,
    }.get(symbol.upper(), 100.0)

    drift = random.uniform(-0.02, 0.05)
    projected = round(base_price * ((1 + drift) ** (days / 30)), 2)
    volatility = round(random.uniform(0.12, 0.28), 3)

    return {
        "symbol": symbol.upper(),
        "horizon_days": days,
        "current_price": base_price,
        "projected_price": projected,
        "expected_return_pct": round(((projected - base_price) / base_price) * 100, 2),
        "volatility": volatility,
        "confidence": "HIGH" if volatility < 0.20 else "MEDIUM",
        "governed_by": "F5 BIG-IP Next on GKE (Host-Device Dataplane)",
    }


def get_balance(account_id: str) -> Dict[str, Any]:
    """Retrieve account balance for a given account ID."""
    return {
        "account_id": account_id,
        "currency": "USD",
        "cash_balance": 450000.00,
        "margin_available": 150000.00,
        "portfolio_valuation": 2480920.50,
        "risk_tier": "TIER_1_ENTERPRISE",
        "governed_by": "F5 BIG-IP Next on GKE (Host-Device Dataplane)",
    }


class BearerAuthMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):
        # Allow healthz and agent-card unauthenticated
        if request.url.path in ("/healthz", "/.well-known/agent-card.json"):
            return await call_next(request)

        auth_header = request.headers.get("authorization", "")
        if not auth_header.startswith("Bearer "):
            logger.warning(f"Rejected unauthenticated request from {request.client.host}")
            return JSONResponse(
                {"error": "Unauthorized. Bearer token required across F5 BNK action gateway."},
                status_code=401,
            )

        token = auth_header[7:].strip()
        if token not in (AGENT_TOKEN, EXTERNAL_TOKEN):
            logger.warning(f"Rejected invalid token request from {request.client.host}")
            return JSONResponse(
                {"error": "Forbidden. Invalid token supplied."},
                status_code=403,
            )

        return await call_next(request)


async def handle_rpc(request: Request) -> Response:
    """Handle JSON-RPC and REST tool execution."""
    try:
        body = await request.json()
    except Exception:
        body = {}

    method = body.get("method")
    params = body.get("params", {})
    req_id = body.get("id", 1)

    # Tool listing
    if method == "tools/list":
        return JSONResponse({
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "tools": [
                    {
                        "name": "forecast",
                        "description": "Forecast symbol price and trend over horizon days",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "symbol": {"type": "string"},
                                "days": {"type": "integer", "default": 30},
                            },
                            "required": ["symbol"],
                        },
                    },
                    {
                        "name": "get_account_balance",
                        "description": "Sensitive tool returning confidential balance",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "account_id": {"type": "string"},
                            },
                            "required": ["account_id"],
                        },
                    },
                ]
            },
        })

    # Tool invocation
    if method == "tools/call":
        tool_name = params.get("name")
        args = params.get("arguments", {})

        if tool_name == "forecast":
            res = calculate_forecast(args.get("symbol", "GOOGL"), int(args.get("days", 30)))
            return JSONResponse({
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {"content": [{"type": "text", "text": json.dumps(res)}]},
            })

        if tool_name == "get_account_balance":
            res = get_balance(args.get("account_id", "ACC-7890"))
            return JSONResponse({
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {"content": [{"type": "text", "text": json.dumps(res)}]},
            })

        return JSONResponse(
            {"jsonrpc": "2.0", "id": req_id, "error": {"code": -32601, "message": f"Tool '{tool_name}' not found"}},
            status_code=404,
        )

    # Direct REST fallback (e.g. POST /forecast)
    if "symbol" in body:
        res = calculate_forecast(body.get("symbol", "GOOGL"), int(body.get("days", 30)))
        return JSONResponse(res)

    return JSONResponse({
        "status": "ready",
        "service": "gke-vertex-finance-mcp",
        "hint": "Use JSON-RPC method tools/call or tools/list",
    })


async def healthz(request: Request) -> Response:
    return JSONResponse({"status": "healthy", "service": "gke-vertex-finance-tool"})


async def agent_card(request: Request) -> Response:
    card = {
        "name": "gke-vertex-finance-tool",
        "description": "Financial forecasting and portfolio service governed by F5 BNK on GKE",
        "version": "1.0.0",
        "url": PUBLIC_URL,
        "capabilities": ["tools"],
        "governance": {
            "gateway": "F5 BIG-IP Next for Kubernetes",
            "cloud": "Google Cloud GKE",
            "agent_framework": "Google Cloud Vertex AI Reasoning Engine",
        },
    }
    return JSONResponse(card)


app = Starlette(
    routes=[
        Route("/healthz", healthz, methods=["GET"]),
        Route("/.well-known/agent-card.json", agent_card, methods=["GET"]),
        Route("/", handle_rpc, methods=["GET", "POST"]),
        Route("/v1/mcp/forecast", handle_rpc, methods=["GET", "POST"]),
    ],
    middleware=[Middleware(BearerAuthMiddleware)],
)

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=LISTEN_PORT)

import json
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def _send_json(self, status: int, obj):
        raw = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        if self.path != "/mcp/rpc":
            self._send_json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length) if length > 0 else b"{}"
        try:
            req = json.loads(body.decode())
        except Exception:
            self._send_json(400, {"error": "bad json"})
            return

        method = req.get("method")
        req_id = req.get("id")

        def ok(result):
            return {"jsonrpc": "2.0", "id": req_id, "result": result}

        if method == "initialize":
            self._send_json(
                200,
                ok(
                    {
                        "protocolVersion": "2024-11-05",
                        "serverInfo": {"name": "nexus-mcp-mock", "version": "0.1.0"},
                        "capabilities": {"tools": {}},
                    }
                ),
            )
            return
        if method == "tools/list":
            self._send_json(
                200,
                ok(
                    {
                        "tools": [
                            {
                                "name": "mcp_repo_probe",
                                "description": "Probe repository paths and return basic stats (mock).",
                                "inputSchema": {
                                    "type": "object",
                                    "properties": {
                                        "glob": {"type": "string"},
                                        "limit": {"type": "integer"},
                                    },
                                    "required": ["glob"],
                                },
                            }
                        ]
                    }
                ),
            )
            return
        if method == "tools/call":
            params = req.get("params") or {}
            name = params.get("name")
            args = params.get("arguments") or {}
            try:
                # arguments may be json.RawMessage string in some clients; accept both dict and str
                if isinstance(args, str):
                    args = json.loads(args)
            except Exception:
                args = {}
            if name == "mcp_repo_probe":
                glob = args.get("glob", "")
                limit = int(args.get("limit", 10) or 10)
                self._send_json(
                    200,
                    ok(
                        {
                            "isError": False,
                            "content": [
                                {
                                    "type": "text",
                                    "text": json.dumps(
                                        {
                                            "glob": glob,
                                            "limit": limit,
                                            "note": "mock server response (does not access filesystem)",
                                            "example_matches": [
                                                "internal/team/runtime.go",
                                                "internal/gateway/server.go",
                                                "cmd/nexus/main.go",
                                            ][:limit],
                                        },
                                        ensure_ascii=False,
                                    ),
                                }
                            ],
                        }
                    ),
                )
                return

            self._send_json(
                200,
                ok(
                    {
                        "isError": True,
                        "content": [{"type": "text", "text": f"unknown tool {name}"}],
                    }
                ),
            )
            return

        # default
        self._send_json(
            200,
            {
                "jsonrpc": "2.0",
                "id": req_id,
                "error": {"code": -32601, "message": f"method {method} not found"},
            },
        )

    def do_GET(self):
        if self.path != "/mcp/sse":
            self.send_response(404)
            self.end_headers()
            return
        payload = json.dumps({"postPath": "/mcp/rpc", "ssePath": "/mcp/sse"})
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "keep-alive")
        self.end_headers()
        self.wfile.write(f"event: endpoint\ndata: {payload}\n\n".encode())


def main():
    server = HTTPServer(("127.0.0.1", 18110), Handler)
    print("mcp mock server listening on http://127.0.0.1:18110")
    server.serve_forever()


if __name__ == "__main__":
    main()


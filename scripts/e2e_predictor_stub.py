#!/usr/bin/env python3
"""Predictor XGBoost falso para el E2E de control (sólo stdlib).

Imita el contrato del sidecar real que consume el backend:
  POST /predict        -> triplete fijo y placeholder
  GET  /admin/modelos  -> catálogo vacío (sin modelos entrenados)
  GET  /health         -> 200
"""
import argparse
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PREDICT = {
    "pctCrudos": 10,
    "pctOk": 80,
    "pctQuemados": 10,
    "modelVersion": "e2e-stub",
    "esPlaceholder": True,
}
MODELOS = {"productos": []}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _send(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        if self.path == "/predict":
            self._send(200, PREDICT)
        else:
            self._send(404, {"error": "not found"})

    def do_GET(self):
        if self.path == "/admin/modelos":
            self._send(200, MODELOS)
        elif self.path == "/health":
            self._send(200, {"status": "ok"})
        else:
            self._send(404, {"error": "not found"})

    def log_message(self, *_args):
        pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=18090)
    args = parser.parse_args()
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()

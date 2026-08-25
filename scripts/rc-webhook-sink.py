#!/usr/bin/env python3
"""One-shot RC webhook receiver that retains only a verified boolean proof."""

import argparse
import hashlib
import hmac
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--secret-file", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    if not args.secret_file.is_file() or args.secret_file.is_symlink() or args.out.exists() or not 1024 <= args.port <= 65535:
        raise SystemExit("invalid RC webhook sink inputs")
    secret = args.secret_file.read_bytes().strip()
    if len(secret) < 32: raise SystemExit("RC webhook proof secret is too short")

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length)
            expected = "sha256=" + hmac.new(secret, body, hashlib.sha256).hexdigest()
            valid = hmac.compare_digest(expected, self.headers.get("X-Astronomer-Signature", ""))
            if valid:
                args.out.write_text(json.dumps({"verified": True, "body_sha256": "sha256:" + hashlib.sha256(body).hexdigest()}, sort_keys=True) + "\n", encoding="utf-8")
                self.send_response(204); self.end_headers()
                self.server.shutdown_requested = True
            else:
                self.send_response(401); self.end_headers()

        def log_message(self, *_):
            return

    server = ThreadingHTTPServer(("0.0.0.0", args.port), Handler)
    server.timeout = 1
    server.shutdown_requested = False
    while not server.shutdown_requested:
        server.handle_request()


if __name__ == "__main__":
    main()

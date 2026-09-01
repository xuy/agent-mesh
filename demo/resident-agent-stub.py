#!/usr/bin/env python3
"""A stand-in for a resident agent's local API, so the webhook delivery mode
can be exercised without installing one.

It mimics the shape OpenClaw's Gateway exposes: a POST endpoint behind a bearer
token that accepts a message for a live session. Two behaviours, chosen by the
path, because real agents do both:

  /sync   answers in the response body      -> the peer's `mesh ask` returns it
  /async  acknowledges only (202)           -> the peer waits for `mesh reply`

Run:  python3 demo/resident-agent-stub.py 8391 test-token
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

TOKEN = sys.argv[2] if len(sys.argv) > 2 else "test-token"


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.headers.get("Authorization") != f"Bearer {TOKEN}":
            self.send_error(401, "bad token")
            return

        msg = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        print(f"[resident] from={msg['from']} id={msg['id']}: {msg['body']}", flush=True)
        print(f"[resident] it told me how to answer: {msg['reply_with']}", flush=True)

        if self.path.endswith("/async"):
            self.send_response(202)
            self.end_headers()
            self.wfile.write(b'{"status":"queued"}')
            return

        answer = f"I am the resident agent. You asked: {msg['body']!r}. Answering from a live session."
        body = json.dumps({"reply": answer}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8391
    print(f"[resident] listening on 127.0.0.1:{port}", flush=True)
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()

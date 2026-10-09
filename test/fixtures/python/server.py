import os
from http.server import BaseHTTPRequestHandler, HTTPServer


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok" if self.path == "/health" else b"hello from python fixture\n")


HTTPServer((os.environ.get("HOST", "127.0.0.1"), int(os.environ.get("PORT", "3000"))), H).serve_forever()

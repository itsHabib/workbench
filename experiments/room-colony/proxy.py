"""Linux-host relay: guests may reach their gateway, not the host's private LAN.

No policy or inference lives here. It forwards only the experiment's HTTP API
to one fixed upstream; the broker authenticates every request.
"""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import re
import sys
import urllib.error
import urllib.request


class Proxy(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def forward(self):
        try:
            if not re.fullmatch(r"/(artifacts|generate)/[a-z0-9-]+", self.path):
                self.send_error(404)
                return
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 <= size <= 65536:
                self.send_error(413)
                return
            request = urllib.request.Request(self.server.upstream + self.path,
                data=self.rfile.read(size) if size else None, method=self.command,
                headers={"Authorization": self.headers.get("Authorization", ""), "Content-Type": "application/json"})
            try:
                response = urllib.request.urlopen(request, timeout=240)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                data = response.read(131073)
                if len(data) > 131072:
                    raise ValueError("upstream response too large")
                self.send_response(response.code)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
        except (OSError, ValueError) as error:
            self.send_error(502, str(error))

    do_GET = forward
    do_POST = forward
    do_PUT = forward


if __name__ == "__main__":
    server = ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1])), Proxy)
    server.upstream = sys.argv[2]
    server.serve_forever()

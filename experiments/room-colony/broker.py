"""Durable transport and a credential-local inference adapter; no scheduling decisions."""
import argparse
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import subprocess
import threading

from workload import digest


def write(path, value):
    tmp = path.with_suffix(".tmp")
    with tmp.open("w") as stream:
        json.dump(value, stream, sort_keys=True, indent=2)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(tmp, path)


class Store:
    def __init__(self, root):
        self.root = root
        self.lock = threading.Lock()
        self.generation_lock = threading.Lock()

    def get(self, key):
        path = self.root / "artifacts" / (key + ".json")
        return json.loads(path.read_text()) if path.exists() else None

    def put(self, key, value):
        with self.lock:
            old = self.get(key)
            if old is not None and old != value:
                raise ValueError("immutable artifact conflict")
            if old is None:
                write(self.root / "artifacts" / (key + ".json"), value)
        return value

    def generate(self, number, value):
        if number not in ("1", "2"):
            raise ValueError("only the two declared rounds are available")
        with self.generation_lock:
            path = self.root / "model" / number
            if path.exists():
                if json.loads((path / "request.json").read_text()) != value:
                    raise ValueError("generation request changed on replay")
                reply = path / "reply.json"
                if not reply.exists():
                    raise ValueError("previous model call incomplete; inspect retained attempt, do not repeat blindly")
                return json.loads(reply.read_text())
            path.mkdir()
            write(path / "request.json", value)
            prompt = (
                "Write only Python source defining plan(points). No markdown, tools, files or external calls. "
                "points is a list of [x,y] integer coordinates. Return a permutation of indices visiting all "
                "points, starting and returning to [0,0]. Minimize total Manhattan distance. Handle empty input, "
                "duplicates and negative coordinates. Use only Python standard library. Keep execution below "
                "one second for 40 points. Improve this implementation; nearest-neighbor and 2-opt are reasonable. "
                "The scorer and held-out maps are fixed outside the code you write.\n"
                + json.dumps(value)
            )
            (path / "prompt.txt").write_text(prompt)
            with (path / "provider.log").open("w") as log:
                result = subprocess.run(
                    ["codex", "exec", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only",
                     "-o", str(path / "response.txt"), prompt], cwd=path, stdin=subprocess.DEVNULL,
                    stdout=log, stderr=log, timeout=220)
            if result.returncode:
                raise ValueError("model provider failed; see local provider.log")
            source = (path / "response.txt").read_text().strip()
            if source.startswith("```"):
                source = "\n".join(source.splitlines()[1:-1])
            source += "\n"
            reply = {"source": source, "sha256": digest(source), "round": int(number)}
            write(path / "reply.json", reply)
            return reply


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def handle_request(self):
        if not hmac.compare_digest(self.headers.get("Authorization", ""), "Bearer " + self.server.token):
            self.send_error(403)
            return
        try:
            match = re.fullmatch(r"/(artifacts|generate)/([a-z0-9-]+)", self.path)
            if not match:
                self.send_error(404)
                return
            namespace, key = match.groups()
            size = int(self.headers.get("Content-Length", "0"))
            if size < 0 or size > 65536:
                raise ValueError("request too large")
            value = json.loads(self.rfile.read(size)) if size else None
            if namespace == "artifacts" and self.command == "GET":
                result = self.server.store.get(key)
                if result is None:
                    self.send_error(404)
                    return
            elif namespace == "artifacts" and self.command == "PUT":
                if not isinstance(value, dict):
                    raise ValueError("artifact must be an object")
                result = self.server.store.put(key, value)
            elif namespace == "generate" and self.command == "POST":
                result = self.server.store.generate(key, value)
            else:
                self.send_error(405)
                return
            data = json.dumps(result).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        except (ValueError, OSError, subprocess.TimeoutExpired) as error:
            self.send_error(409, str(error))

    do_GET = handle_request
    do_PUT = handle_request
    do_POST = handle_request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=Path)
    parser.add_argument("--port", type=int, default=0)
    args = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.token = (args.root / "token").read_text().strip()
    server.store = Store(args.root)
    write(args.root / "broker.json", {"pid": os.getpid(), "port": server.server_port})
    server.serve_forever()


if __name__ == "__main__":
    main()

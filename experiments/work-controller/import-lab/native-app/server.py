#!/usr/bin/env python3
"""Local browser-first CSV import desk."""
import argparse
import csv
import hashlib
import io
import json
import os
import sqlite3
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

FIELDS = ("id", "name", "email")
MAX_BODY_BYTES = 10 * 1024 * 1024


def validate_csv(source):
    records, errors = [], []
    try:
        reader = csv.DictReader(io.StringIO(source, newline=""), strict=True)
        if reader.fieldnames is None:
            return records, [{"row": 1, "message": "CSV must include a header row"}]
        headers = [value.strip() if value else "" for value in reader.fieldnames]
        missing = [field for field in FIELDS if field not in headers]
        if missing:
            return records, [{"row": 1, "message": "missing required column(s): " + ", ".join(missing)}]
        for number, row in enumerate(reader, 2):
            if None in row:
                errors.append({"row": number, "message": "too many columns"})
                continue
            values = {field: (row.get(field) or "").strip() for field in FIELDS}
            missing_values = [field for field, value in values.items() if not value]
            if missing_values:
                errors.append({"row": number, "message": "missing " + ", ".join(missing_values)})
                continue
            email = values["email"]
            if " " in email:
                errors.append({"row": number, "message": "email must not contain spaces"})
                continue
            if email.count("@") != 1:
                errors.append({"row": number, "message": "email must contain one @ with text on both sides"})
                continue
            local, domain = email.split("@")
            if not local or not domain:
                errors.append({"row": number, "message": "email must contain text on both sides of @"})
                continue
            records.append(values)
    except csv.Error as exc:
        return [], [{"row": 1, "message": "invalid CSV: " + str(exc)}]
    return records, errors


class RequestConflict(Exception):
    """A request ID was already used for different CSV input."""


class ImportStore:
    def __init__(self, data_dir=None):
        self.lock = threading.RLock()
        if data_dir is None:
            database = ":memory:"
        else:
            os.makedirs(data_dir, exist_ok=True)
            database = os.path.join(data_dir, "imports.sqlite3")
        self.connection = sqlite3.connect(database, check_same_thread=False)
        self.connection.row_factory = sqlite3.Row
        self.connection.execute("PRAGMA busy_timeout = 5000")
        self.connection.execute("""CREATE TABLE IF NOT EXISTS imports (
            id TEXT PRIMARY KEY, status TEXT NOT NULL, records TEXT NOT NULL,
            errors TEXT NOT NULL, csv TEXT NOT NULL, request_id TEXT UNIQUE,
            fingerprint TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
        )""")
        self.connection.commit()

    @staticmethod
    def _item(row):
        if row is None:
            return None
        return {"id": row["id"], "status": row["status"], "records": json.loads(row["records"]),
                "errors": json.loads(row["errors"]), "csv": row["csv"], "request_id": row["request_id"]}

    def create(self, source, request_id=None):
        records, errors = validate_csv(source)
        fingerprint = hashlib.sha256(source.encode("utf-8")).hexdigest()
        item = {"id": uuid.uuid4().hex[:12], "status": "completed" if not errors else "failed",
                "records": records, "errors": errors, "csv": source, "request_id": request_id}
        with self.lock:
            self.connection.execute("BEGIN IMMEDIATE")
            try:
                if request_id is not None:
                    prior = self.connection.execute("SELECT * FROM imports WHERE request_id = ?", (request_id,)).fetchone()
                    if prior is not None:
                        if prior["fingerprint"] != fingerprint:
                            raise RequestConflict("request_id was already used with different CSV")
                        self.connection.commit()
                        return self._item(prior)
                self.connection.execute(
                    "INSERT INTO imports (id,status,records,errors,csv,request_id,fingerprint) VALUES (?,?,?,?,?,?,?)",
                    (item["id"], item["status"], json.dumps(records), json.dumps(errors), source, request_id, fingerprint))
                self.connection.commit()
            except Exception:
                self.connection.rollback()
                raise
        return item

    def get(self, import_id):
        with self.lock:
            row = self.connection.execute("SELECT * FROM imports WHERE id = ?", (import_id,)).fetchone()
        return self._item(row)

    def summaries(self):
        with self.lock:
            rows = self.connection.execute("SELECT id,status,records,errors FROM imports ORDER BY rowid DESC").fetchall()
        return [{"id": row["id"], "status": row["status"], "record_count": len(json.loads(row["records"])),
                 "error_count": len(json.loads(row["errors"]))} for row in rows]

    def close(self):
        with self.lock:
            self.connection.close()


PAGE = """<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Import desk</title><style>
:root{--ink:#17212b;--muted:#64717d;--line:#dce3e8;--blue:#246b8f;--pale:#f4f8fa;--red:#a33b3b;--green:#247653}*{box-sizing:border-box}body{margin:0;background:#eef2f4;color:var(--ink);font:15px/1.45 system-ui,-apple-system,sans-serif}main{max-width:980px;margin:auto;padding:38px 20px 60px}.eyebrow{color:var(--blue);font-size:12px;font-weight:700;letter-spacing:.13em;text-transform:uppercase}h1{font:600 34px/1.1 Georgia,serif;margin:7px 0 8px}p{color:var(--muted);margin:0 0 24px}.grid{display:grid;grid-template-columns:minmax(0,1fr) 310px;gap:18px}.card{background:#fff;border:1px solid var(--line);border-radius:13px;box-shadow:0 7px 25px #24313c0b;padding:22px}h2{font-size:16px;margin:0 0 7px}label{display:block;font-size:13px;font-weight:700;margin:16px 0 7px}input[type=file]{display:block;width:100%;font-size:13px}textarea{display:block;width:100%;min-height:245px;border:1px solid #b9c7d0;border-radius:8px;padding:12px;font:13px/1.5 monospace;resize:vertical}button{border:0;border-radius:7px;background:var(--blue);color:white;font-weight:700;padding:10px 15px;cursor:pointer}button.secondary{background:#e8f0f4;color:#20546f}button:disabled{opacity:.55;cursor:wait}.actions{display:flex;align-items:center;gap:10px;margin-top:14px}.hint,.record-count{font-size:12px;color:var(--muted)}.import{border-top:1px solid var(--line);padding:13px 0}.import:first-child{border-top:0;padding-top:0}.row{display:flex;justify-content:space-between;gap:12px;align-items:center}.id{font:12px monospace;color:var(--muted)}.badge{border-radius:99px;padding:3px 8px;font-size:11px;font-weight:800;text-transform:uppercase}.completed{background:#e4f3eb;color:var(--green)}.failed{background:#fbe9e7;color:var(--red)}.detail{margin-top:14px;background:var(--pale);border-radius:8px;padding:12px;font-size:13px}.detail ul{margin:7px 0 0;padding-left:21px;color:var(--red)}.detail table{border-collapse:collapse;margin-top:8px;width:100%;font-size:12px}.detail th,.detail td{text-align:left;border-bottom:1px solid var(--line);padding:4px 6px}.empty{color:var(--muted);font-size:13px;padding:14px 0}@media(max-width:740px){.grid{grid-template-columns:1fr}main{padding-top:25px}}
</style></head><body><main><div class="eyebrow">Local data utility</div><h1>Import desk</h1><p>Validate a CSV, keep good rows, and repair the ones that need attention.</p><div class="grid"><section class="card"><h2>New import</h2><div class="hint">Required columns: id, name, email</div><label for="file">Choose a CSV file</label><input id="file" type="file" accept=".csv,text/csv"><label for="csv">CSV records</label><textarea id="csv" spellcheck="false">id,name,email
1001,Ada Lovelace,ada@example.com
1002,Grace Hopper,grace@example.com</textarea><div class="actions"><button id="submit" type="button">Validate import</button><span class="hint" id="message" aria-live="polite"></span></div></section><aside class="card"><div class="row"><h2>Recent imports</h2><button class="secondary" id="refresh" type="button">Refresh</button></div><div id="imports" class="empty">Loading...</div></aside></div><section id="selected" class="card" hidden style="margin-top:18px"></section></main><script>
(() => {
  const csv = document.querySelector('#csv'), file = document.querySelector('#file'), submit = document.querySelector('#submit'), message = document.querySelector('#message'), imports = document.querySelector('#imports'), selected = document.querySelector('#selected');
  const esc = (v) => String(v).replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const request = (url, options) => fetch(url, options).then((r) => r.json().then((d) => ({response:r, data:d})));
  const show = (id) => request('/imports/' + encodeURIComponent(id)).then(({data}) => { selected.hidden = false; const rows = data.records.slice(0, 200).map((r) => '<tr><td>'+esc(r.id)+'</td><td>'+esc(r.name)+'</td><td>'+esc(r.email)+'</td></tr>').join(''); const more = data.records.length > 200 ? '<p class="hint">Showing first 200 of '+data.records.length+' valid records.</p>' : ''; const errors = data.errors.length ? '<div class="detail"><strong>Rows to repair</strong><ul>'+data.errors.map((e) => '<li>Row '+esc(e.row)+': '+esc(e.message)+'</li>').join('')+'</ul><button id="retry" type="button" style="margin-top:12px">Load CSV to repair</button></div>' : '<div class="detail" style="color:var(--green)">All rows passed validation.</div>'; const records = data.records.length ? '<div class="detail"><strong>Valid rows</strong><table><thead><tr><th>ID</th><th>Name</th><th>Email</th></tr></thead><tbody>'+rows+'</tbody></table>'+more+'</div>' : ''; selected.innerHTML = '<div class="row"><h2>Import <span class="id">'+esc(data.id)+'</span></h2><span class="badge '+esc(data.status)+'">'+esc(data.status)+'</span></div><div class="record-count">'+data.records.length+' valid record(s), '+data.errors.length+' error(s)</div>'+records+errors; const retry = document.querySelector('#retry'); if (retry) retry.onclick = () => { csv.value = data.csv; csv.focus(); message.textContent = 'Correct the rows, then validate again.'; }; selected.scrollIntoView({behavior:'smooth',block:'nearest'}); });
  const load = () => request('/imports').then(({data}) => { if (!data.imports.length) { imports.textContent = 'No imports yet.'; return; } imports.innerHTML = data.imports.map((i) => '<div class="import"><div class="row"><span class="id">'+esc(i.id)+'</span><span class="badge '+esc(i.status)+'">'+esc(i.status)+'</span></div><div class="record-count">'+i.record_count+' valid, '+i.error_count+' errors</div><button class="secondary inspect" data-id="'+esc(i.id)+'" type="button" style="margin-top:8px;padding:6px 9px;font-size:12px">Inspect</button></div>').join(''); imports.querySelectorAll('.inspect').forEach((b) => { b.onclick = () => show(b.dataset.id); }); });
  file.onchange = () => { const chosen = file.files[0]; if (!chosen) return; const reader = new FileReader(); reader.onload = () => { csv.value = reader.result; message.textContent = 'Loaded '+chosen.name+'.'; }; reader.onerror = () => { message.textContent = 'Could not read that file.'; }; reader.readAsText(chosen); };
  submit.onclick = () => { submit.disabled = true; message.textContent = 'Validating...'; request('/imports', {method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({csv:csv.value})}).then(({response,data}) => { if (!response.ok) { message.textContent = data.error || 'Import failed.'; return; } message.textContent = 'Import '+data.status+'.'; return load().then(() => show(data.id)); }).catch(() => { message.textContent = 'Could not reach the local server.'; }).finally(() => { submit.disabled = false; }); };
  document.querySelector('#refresh').onclick = load; load();
})();
</script></body></html>"""


class Handler(BaseHTTPRequestHandler):
    store = ImportStore()

    def send_payload(self, status, payload, content_type="application/json"):
        body = payload.encode() if isinstance(payload, str) else json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", content_type + "; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = urlparse(self.path).path
        if path == "/health":
            self.send_payload(200, {"ok": True})
            return
        if path == "/":
            self.send_payload(200, PAGE, "text/html")
            return
        if path == "/imports":
            self.send_payload(200, {"imports": self.store.summaries()})
            return
        if path.startswith("/imports/"):
            item = self.store.get(path.split("/", 2)[2])
            if item is None:
                self.send_payload(404, {"error": "import not found"})
                return
            self.send_payload(200, {key: item[key] for key in ("id", "status", "records", "errors", "csv")})
            return
        self.send_payload(404, {"error": "not found"})

    def do_POST(self):
        if urlparse(self.path).path != "/imports":
            self.send_payload(404, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            self.send_payload(400, {"error": "invalid Content-Length"})
            return
        if length < 0:
            self.send_payload(400, {"error": "invalid Content-Length"})
            return
        if length > MAX_BODY_BYTES:
            remaining = length
            while remaining:
                chunk = self.rfile.read(min(64 * 1024, remaining))
                if not chunk:
                    break
                remaining -= len(chunk)
            self.send_payload(413, {"error": "request body exceeds 10 MiB limit"})
            return
        try:
            body = self.rfile.read(length)
            if len(body) > MAX_BODY_BYTES:
                self.send_payload(413, {"error": "request body exceeds 10 MiB limit"})
                return
            payload = json.loads(body)
            if not isinstance(payload, dict):
                raise ValueError("JSON body must be an object")
            source = payload.get("csv")
            if not isinstance(source, str):
                raise ValueError("csv must be a string")
            request_id = payload.get("request_id")
            if request_id is not None and not isinstance(request_id, str):
                raise ValueError("request_id must be a string")
        except (ValueError, json.JSONDecodeError, UnicodeDecodeError) as exc:
            self.send_payload(400, {"error": str(exc)})
            return
        try:
            item = self.store.create(source, request_id)
        except RequestConflict as exc:
            self.send_payload(409, {"error": str(exc)})
            return
        self.send_payload(201, {"id": item["id"], "status": item["status"]})

    def log_message(self, _format, *_args):
        return


def main():
    parser = argparse.ArgumentParser(description="Run the local import desk")
    parser.add_argument("--port", type=int, default=8000)
    parser.add_argument("--data-dir", default=".", help="directory for the SQLite import store")
    args = parser.parse_args()
    Handler.store = ImportStore(args.data_dir)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print("Import desk listening on http://127.0.0.1:%d" % args.port, flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
        Handler.store.close()


if __name__ == "__main__":
    main()

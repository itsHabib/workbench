#!/usr/bin/env python3
"""Local browser-first CSV import desk."""
import argparse
import csv
import io
import json
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

FIELDS = ("id", "name", "email")


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
        errors.append({"row": 1, "message": "invalid CSV: " + str(exc)})
    return records, errors


class ImportStore:
    def __init__(self):
        self.items, self.lock = {}, threading.Lock()

    def create(self, source, request_id=None):
        records, errors = validate_csv(source)
        item = {"id": uuid.uuid4().hex[:12], "status": "completed" if not errors else "failed",
                "records": records, "errors": errors, "csv": source, "request_id": request_id}
        with self.lock:
            self.items[item["id"]] = item
        return item

    def get(self, import_id):
        with self.lock:
            return self.items.get(import_id)

    def summaries(self):
        with self.lock:
            return [{"id": item["id"], "status": item["status"], "record_count": len(item["records"]),
                     "error_count": len(item["errors"])} for item in reversed(list(self.items.values()))]


PAGE = """<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Import desk</title><style>
:root{--ink:#17212b;--muted:#64717d;--line:#dce3e8;--blue:#246b8f;--pale:#f4f8fa;--red:#a33b3b;--green:#247653}*{box-sizing:border-box}body{margin:0;background:#eef2f4;color:var(--ink);font:15px/1.45 system-ui,-apple-system,sans-serif}main{max-width:980px;margin:auto;padding:38px 20px 60px}.eyebrow{color:var(--blue);font-size:12px;font-weight:700;letter-spacing:.13em;text-transform:uppercase}h1{font:600 34px/1.1 Georgia,serif;margin:7px 0 8px}p{color:var(--muted);margin:0 0 24px}.grid{display:grid;grid-template-columns:minmax(0,1fr) 310px;gap:18px}.card{background:#fff;border:1px solid var(--line);border-radius:13px;box-shadow:0 7px 25px #24313c0b;padding:22px}h2{font-size:16px;margin:0 0 7px}label{display:block;font-size:13px;font-weight:700;margin:16px 0 7px}textarea{display:block;width:100%;min-height:245px;border:1px solid #b9c7d0;border-radius:8px;padding:12px;font:13px/1.5 monospace;resize:vertical}button{border:0;border-radius:7px;background:var(--blue);color:white;font-weight:700;padding:10px 15px;cursor:pointer}button.secondary{background:#e8f0f4;color:#20546f}button:disabled{opacity:.55;cursor:wait}.actions{display:flex;align-items:center;gap:10px;margin-top:14px}.hint,.record-count{font-size:12px;color:var(--muted)}.import{border-top:1px solid var(--line);padding:13px 0}.import:first-child{border-top:0;padding-top:0}.row{display:flex;justify-content:space-between;gap:12px;align-items:center}.id{font:12px monospace;color:var(--muted)}.badge{border-radius:99px;padding:3px 8px;font-size:11px;font-weight:800;text-transform:uppercase}.completed{background:#e4f3eb;color:var(--green)}.failed{background:#fbe9e7;color:var(--red)}.detail{margin-top:14px;background:var(--pale);border-radius:8px;padding:12px;font-size:13px}.detail ul{margin:7px 0 0;padding-left:21px;color:var(--red)}.empty{color:var(--muted);font-size:13px;padding:14px 0}@media(max-width:740px){.grid{grid-template-columns:1fr}main{padding-top:25px}}
+</style></head><body><main><div class="eyebrow">Local data utility</div><h1>Import desk</h1><p>Validate a small CSV, keep good rows, and repair the ones that need attention.</p><div class="grid"><section class="card"><h2>New import</h2><div class="hint">Required columns: id, name, email</div><label for="csv">CSV records</label><textarea id="csv" spellcheck="false">id,name,email
1001,Ada Lovelace,ada@example.com
1002,Grace Hopper,grace@example.com</textarea><div class="actions"><button id="submit">Validate import</button><span class="hint" id="message" aria-live="polite"></span></div></section><aside class="card"><div class="row"><h2>Recent imports</h2><button class="secondary" id="refresh">Refresh</button></div><div id="imports" class="empty">Loading…</div></aside></div><section id="selected" class="card" style="display:none;margin-top:18px"></section></main><script>
+const csv=document.querySelector('#csv'),submit=document.querySelector('#submit'),message=document.querySelector('#message'),imports=document.querySelector('#imports'),selected=document.querySelector('#selected');
+const esc=v=>String(v).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
+async function load(){const r=await fetch('/imports'),d=await r.json();if(!d.imports.length){imports.textContent='No imports yet.';return}imports.innerHTML=d.imports.map(i=>`<div class="import"><div class="row"><span class="id">${esc(i.id)}</span><span class="badge ${i.status}">${esc(i.status)}</span></div><div class="record-count">${i.record_count} valid · ${i.error_count} errors</div><button class="secondary" style="margin-top:8px;padding:6px 9px;font-size:12px" onclick="show('${esc(i.id)}')">Inspect</button></div>`).join('')}
+async function show(id){const r=await fetch('/imports/'+encodeURIComponent(id)),i=await r.json();selected.style.display='block';selected.innerHTML=`<div class="row"><h2>Import <span class="id">${esc(i.id)}</span></h2><span class="badge ${i.status}">${esc(i.status)}</span></div><div class="record-count">${i.records.length} valid record(s)</div>${i.errors.length?`<div class="detail"><strong>Rows to repair</strong><ul>${i.errors.map(e=>`<li>Row ${esc(e.row)}: ${esc(e.message)}</li>`).join('')}</ul><button style="margin-top:12px" onclick="retry('${esc(i.id)}')">Load CSV to repair</button></div>`:'<div class="detail" style="color:var(--green)">All rows passed validation.</div>'}`;selected.scrollIntoView({behavior:'smooth',block:'nearest'})}
+async function retry(id){const r=await fetch('/imports/'+encodeURIComponent(id)),i=await r.json();csv.value=i.csv;csv.focus();message.textContent='Correct the rows, then validate again.'}
+submit.onclick=async()=>{submit.disabled=true;message.textContent='Validating…';try{const r=await fetch('/imports',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({csv:csv.value})}),i=await r.json();message.textContent=`Import ${i.status}.`;await load();await show(i.id)}catch(e){message.textContent='Could not reach the local server.'}finally{submit.disabled=false}};document.querySelector('#refresh').onclick=load;load();
+</script></body></html>"""


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
            payload = json.loads(self.rfile.read(length))
            source = payload.get("csv")
            if not isinstance(source, str):
                raise ValueError("csv must be a string")
        except (ValueError, json.JSONDecodeError) as exc:
            self.send_payload(400, {"error": str(exc)})
            return
        item = self.store.create(source, payload.get("request_id"))
        self.send_payload(201, {"id": item["id"], "status": item["status"]})

    def log_message(self, _format, *_args):
        return


def main():
    parser = argparse.ArgumentParser(description="Run the local import desk")
    parser.add_argument("--port", type=int, default=8000)
    parser.add_argument("--data-dir", default=".", help="reserved launch seam for local data")
    args = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print("Import desk listening on http://127.0.0.1:%d" % args.port, flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()

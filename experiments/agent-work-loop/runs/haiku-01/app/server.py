#!/usr/bin/env python3
"""Small stdlib-only import app seed for the live-agent workload.

This is intentionally incomplete.  The workload agents must turn the route
stubs into a durable import service while keeping the documented contract.
"""

import argparse
import csv
import io
import json
import sqlite3
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


SCHEMA = """
CREATE TABLE IF NOT EXISTS imports (
    import_id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT NOT NULL UNIQUE,
    source_format TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)
"""

HTML_TEMPLATE = """<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Document imports</title>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
            max-width: 900px;
            margin: 0 auto;
            padding: 20px;
            background: #f5f5f5;
            color: #333;
        }
        h1 { color: #222; margin-bottom: 30px; }
        .upload-section {
            background: white;
            padding: 30px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            margin-bottom: 30px;
        }
        .file-input-wrapper {
            position: relative;
            display: inline-block;
            width: 100%;
        }
        input[type="file"] {
            position: absolute;
            opacity: 0;
            width: 0;
            height: 0;
        }
        .file-label {
            display: block;
            padding: 20px;
            background: #f0f7ff;
            border: 2px dashed #0066cc;
            border-radius: 4px;
            text-align: center;
            cursor: pointer;
            transition: background 0.2s;
        }
        .file-label:hover {
            background: #e6f2ff;
        }
        .file-label.dragover {
            background: #cce5ff;
            border-color: #0052a3;
        }
        .file-label strong { display: block; color: #0066cc; margin-bottom: 5px; }
        .file-label span { display: block; color: #666; font-size: 0.9em; }
        .status-message {
            margin-top: 15px;
            padding: 10px 15px;
            border-radius: 4px;
            display: none;
        }
        .status-message.success {
            background: #d4edda;
            color: #155724;
            border: 1px solid #c3e6cb;
        }
        .status-message.error {
            background: #f8d7da;
            color: #721c24;
            border: 1px solid #f5c6cb;
        }
        .status-message.loading {
            background: #e2e3e5;
            color: #383d41;
            border: 1px solid #d6d8db;
        }
        .data-section {
            background: white;
            padding: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            display: none;
        }
        .data-section.show { display: block; }
        .data-section h2 {
            margin-top: 0;
            font-size: 1.3em;
            color: #222;
        }
        table {
            width: 100%;
            border-collapse: collapse;
            margin-bottom: 20px;
        }
        th {
            background: #f5f5f5;
            padding: 12px;
            text-align: left;
            font-weight: 600;
            border-bottom: 2px solid #ddd;
        }
        td {
            padding: 12px;
            border-bottom: 1px solid #eee;
        }
        tr.error-row { background: #fff5f5; }
        tr.error-row td { color: #721c24; }
        .error-badge {
            display: inline-block;
            background: #dc3545;
            color: white;
            padding: 3px 8px;
            border-radius: 3px;
            font-size: 0.85em;
            margin-left: 10px;
        }
        .summary {
            padding: 15px;
            background: #f5f5f5;
            border-radius: 4px;
            margin-bottom: 20px;
        }
        .summary-stat {
            display: inline-block;
            margin-right: 20px;
            font-weight: 500;
        }
        .summary-stat .value {
            display: block;
            font-size: 1.4em;
            color: #0066cc;
        }
        .error-list {
            background: #f8d7da;
            border: 1px solid #f5c6cb;
            border-radius: 4px;
            padding: 15px;
            margin-top: 15px;
        }
        .error-list h3 { margin-top: 0; color: #721c24; }
        .error-list ul { margin: 0; padding-left: 20px; }
        .error-list li { color: #721c24; margin-bottom: 5px; }
        @media (max-width: 600px) {
            body { padding: 15px; }
            .upload-section { padding: 20px; }
            table { font-size: 0.9em; }
            th, td { padding: 8px; }
            .summary-stat { display: block; margin-bottom: 10px; }
        }
    </style>
</head>
<body>
    <h1>Document imports</h1>
    <div class="upload-section">
        <div class="file-input-wrapper">
            <input type="file" id="csvFile" accept=".csv" />
            <label for="csvFile" class="file-label">
                <strong>Choose CSV file or drag here</strong>
                <span>Upload a CSV file with headers and data rows</span>
            </label>
        </div>
        <div id="status" class="status-message"></div>
    </div>
    <div id="dataSection" class="data-section">
        <h2>Import Results</h2>
        <div id="summary" class="summary"></div>
        <table id="dataTable">
            <thead id="tableHead"></thead>
            <tbody id="tableBody"></tbody>
        </table>
    </div>
    <script>
        const fileInput = document.getElementById('csvFile');
        const fileLabel = document.querySelector('.file-label');
        const statusMsg = document.getElementById('status');
        const dataSection = document.getElementById('dataSection');
        const summaryDiv = document.getElementById('summary');
        const tableHead = document.getElementById('tableHead');
        const tableBody = document.getElementById('tableBody');

        const setStatus = (message, type) => {
            statusMsg.textContent = message;
            statusMsg.className = 'status-message ' + type;
            statusMsg.style.display = 'block';
        };

        const parseCSV = (text) => {
            const rows = [];
            const headers = [];
            let currentRow = [];
            let currentCell = '';
            let insideQuotes = false;

            for (let i = 0; i < text.length; i++) {
                const char = text[i];
                const nextChar = text[i + 1];

                if (char === '"') {
                    if (insideQuotes && nextChar === '"') {
                        currentCell += '"';
                        i++;
                    } else {
                        insideQuotes = !insideQuotes;
                    }
                } else if (char === ',' && !insideQuotes) {
                    currentRow.push(currentCell.trim());
                    currentCell = '';
                } else if ((char === '\\n' || char === '\\r') && !insideQuotes) {
                    if (char === '\\r' && nextChar === '\\n') {
                        i++;
                    }
                    if (currentCell || currentRow.length > 0) {
                        currentRow.push(currentCell.trim());
                        if (headers.length === 0) {
                            headers.push(...currentRow);
                        } else {
                            const row = {};
                            headers.forEach((header, index) => {
                                row[header] = currentRow[index] || '';
                            });
                            rows.push(row);
                        }
                        currentRow = [];
                        currentCell = '';
                    }
                } else {
                    currentCell += char;
                }
            }

            if (currentCell || currentRow.length > 0) {
                currentRow.push(currentCell.trim());
                if (headers.length === 0) {
                    headers.push(...currentRow);
                } else {
                    const row = {};
                    headers.forEach((header, index) => {
                        row[header] = currentRow[index] || '';
                    });
                    rows.push(row);
                }
            }

            return { headers, rows };
        };

        const displayResults = (headers, rows) => {
            tableHead.innerHTML = '<tr>' + headers.map(h => '<th>' + h + '</th>').join('') + '</tr>';
            tableBody.innerHTML = rows.map((row, idx) => {
                const rowClass = row._error ? 'error-row' : '';
                const cells = headers.map(h => '<td>' + (row[h] || '') + '</td>').join('');
                const errorCell = row._error ? '<td><span class="error-badge">Error</span></td>' : '';
                return '<tr class="' + rowClass + '">' + cells + errorCell + '</tr>';
            }).join('');

            const errorCount = rows.filter(r => r._error).length;
            const successCount = rows.length - errorCount;
            summaryDiv.innerHTML = '<div class="summary-stat">Total rows: <span class="value">' + rows.length + '</span></div>' +
                                   '<div class="summary-stat">Valid: <span class="value" style="color: #28a745;">' + successCount + '</span></div>' +
                                   '<div class="summary-stat">Errors: <span class="value" style="color: #dc3545;">' + errorCount + '</span></div>';
            dataSection.classList.add('show');
        };

        const uploadCSV = async (file) => {
            const text = await file.text();
            const { headers, rows } = parseCSV(text);

            if (headers.length === 0) {
                setStatus('No data found in CSV', 'error');
                return;
            }

            setStatus('Uploading...', 'loading');

            try {
                const response = await fetch('/imports', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        request_id: 'upload-' + Date.now(),
                        csv: text
                    })
                });

                const result = await response.json();

                if (response.ok) {
                    displayResults(headers, rows);
                    setStatus('Imported ' + rows.length + ' rows successfully!', 'success');
                } else {
                    setStatus('Upload failed: ' + result.error, 'error');
                }
            } catch (error) {
                setStatus('Upload error: ' + error.message, 'error');
            }
        };

        fileInput.addEventListener('change', (e) => {
            if (e.target.files.length > 0) {
                uploadCSV(e.target.files[0]);
            }
        });

        fileLabel.addEventListener('dragover', (e) => {
            e.preventDefault();
            fileLabel.classList.add('dragover');
        });

        fileLabel.addEventListener('dragleave', () => {
            fileLabel.classList.remove('dragover');
        });

        fileLabel.addEventListener('drop', (e) => {
            e.preventDefault();
            fileLabel.classList.remove('dragover');
            if (e.dataTransfer.files.length > 0) {
                const file = e.dataTransfer.files[0];
                if (file.name.endsWith('.csv')) {
                    uploadCSV(file);
                } else {
                    setStatus('Please upload a CSV file', 'error');
                }
            }
        });
    </script>
</body>
</html>
"""


def initialize_database(state_dir: Path) -> Path:
    state_dir.mkdir(parents=True, exist_ok=True)
    database = state_dir / "imports.sqlite"
    with sqlite3.connect(database, timeout=5.0) as connection:
        connection.execute(SCHEMA)
    return database


def validate_csv(csv_data: str) -> bool:
    """Validate CSV format: must have title,body headers and complete rows."""
    try:
        lines = csv_data.strip().split('\n')
        if not lines:
            return False
        reader = csv.reader(io.StringIO(csv_data))
        rows = list(reader)
        if not rows or len(rows) < 1:
            return False
        headers = rows[0]
        if headers != ["title", "body"]:
            return False
        if len(rows) > 1 and any(len(row) != 2 for row in rows[1:]):
            return False
        return True
    except Exception:
        return False


def validate_documents(documents: object) -> bool:
    """Validate documents array format."""
    if not isinstance(documents, list):
        return False
    for doc in documents:
        if not isinstance(doc, dict):
            return False
        if "title" not in doc or "body" not in doc:
            return False
        if not isinstance(doc["title"], str) or not isinstance(doc["body"], str):
            return False
    return len(documents) > 0


def documents_to_csv(documents: list[dict]) -> str:
    """Convert documents list to CSV format with proper escaping."""
    output = io.StringIO()
    writer = csv.writer(output)
    writer.writerow(["title", "body"])
    for doc in documents:
        writer.writerow([doc["title"], doc["body"]])
    return output.getvalue()


def csv_to_documents(csv_data: str) -> list[dict]:
    """Convert CSV to documents list, expecting title and body columns."""
    reader = csv.DictReader(io.StringIO(csv_data))
    documents = []
    if reader.fieldnames is None or "title" not in reader.fieldnames or "body" not in reader.fieldnames:
        raise ValueError("CSV must have 'title' and 'body' columns")
    for row in reader:
        documents.append({"title": row["title"], "body": row["body"]})
    return documents


class Handler(BaseHTTPRequestHandler):
    database: Path

    def send_json(self, status: int, value: object) -> None:
        body = json.dumps(value, sort_keys=True).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/health":
            self.send_json(200, {"ok": True})
            return
        if self.path == "/":
            body = HTML_TEMPLATE.encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path.startswith("/imports/") and self.path.endswith("/export"):
            import_id = self.path[9:-7]
            try:
                with sqlite3.connect(self.database, timeout=5.0) as connection:
                    cursor = connection.execute(
                        "SELECT import_id, request_id, source_format, payload, created_at FROM imports WHERE import_id = ?",
                        (import_id,)
                    )
                    row = cursor.fetchone()
                    if row is None:
                        self.send_json(404, {"error": "import not found"})
                        return
                    source_format, payload = row[2], row[3]
                    if source_format == "csv":
                        csv_output = payload
                    elif source_format == "json":
                        documents = json.loads(payload)
                        csv_output = documents_to_csv(documents)
                    else:
                        self.send_json(500, {"error": f"unknown format: {source_format}"})
                        return
                    body = csv_output.encode("utf-8")
                    self.send_response(200)
                    self.send_header("Content-Type", "text/csv; charset=utf-8")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
            except Exception as e:
                self.send_json(500, {"error": str(e)})
            return
        if self.path.startswith("/imports/"):
            import_id = self.path[9:]
            try:
                with sqlite3.connect(self.database, timeout=5.0) as connection:
                    cursor = connection.execute(
                        "SELECT import_id, request_id, source_format, payload, created_at FROM imports WHERE import_id = ?",
                        (import_id,)
                    )
                    row = cursor.fetchone()
                    if row is None:
                        self.send_json(404, {"error": "import not found"})
                        return
                    source_format, payload = row[2], row[3]
                    if source_format == "csv":
                        documents = csv_to_documents(payload)
                    elif source_format == "json":
                        documents = json.loads(payload)
                    else:
                        self.send_json(500, {"error": f"unknown format: {source_format}"})
                        return
                    self.send_json(200, {"documents": documents})
            except Exception as e:
                self.send_json(500, {"error": str(e)})
            return
        self.send_json(404, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/imports":
            self.send_json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        try:
            request = json.loads(self.rfile.read(length))
        except (json.JSONDecodeError, UnicodeDecodeError):
            self.send_json(400, {"error": "invalid JSON"})
            return
        if not isinstance(request, dict) or not request.get("request_id"):
            self.send_json(400, {"error": "request_id is required"})
            return

        request_id = request.get("request_id")
        csv_data = request.get("csv")
        documents = request.get("documents")

        if not csv_data and not documents:
            self.send_json(400, {"error": "csv or documents is required"})
            return

        source_format = None
        payload = None

        if csv_data:
            if not validate_csv(csv_data):
                self.send_json(400, {"error": "malformed CSV"})
                return
            source_format = "csv"
            payload = csv_data
        elif documents:
            if not validate_documents(documents):
                self.send_json(400, {"error": "malformed documents"})
                return
            source_format = "json"
            payload = json.dumps(documents)

        try:
            with sqlite3.connect(self.database, timeout=5.0) as connection:
                cursor = connection.execute(
                    "INSERT INTO imports (request_id, source_format, payload) VALUES (?, ?, ?)",
                    (request_id, source_format, payload)
                )
                connection.commit()
                import_id = cursor.lastrowid
                self.send_json(201, {"import_id": import_id, "request_id": request_id})
        except sqlite3.IntegrityError:
            with sqlite3.connect(self.database, timeout=5.0) as connection:
                cursor = connection.execute(
                    "SELECT import_id, source_format, payload FROM imports WHERE request_id = ?",
                    (request_id,)
                )
                existing = cursor.fetchone()
                if existing and existing[1] == source_format and existing[2] == payload:
                    self.send_json(200, {"import_id": existing[0], "request_id": request_id})
                else:
                    self.send_json(409, {"error": "request_id already exists"})
        except sqlite3.OperationalError as e:
            if "database is locked" in str(e):
                self.send_json(503, {"error": "database is locked"})
            else:
                self.send_json(500, {"error": str(e)})
        except Exception as e:
            self.send_json(500, {"error": str(e)})

    def log_message(self, format: str, *args: object) -> None:
        return


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--state", type=Path, required=True)
    args = parser.parse_args()
    Handler.database = initialize_database(args.state)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()

# Import desk

Build a small local app: upload CSV records, validate them, inspect the result and retry failed
input. A user should be able to use the browser without curl. Bind only to 127.0.0.1. Python 3
standard library backend; plain HTML/CSS/JS frontend, no package install or external assets.

Input fields: id,name,email. Valid: all fields nonempty, email has a nonempty part on each side
of one @ and no spaces. Invalid rows should have a useful row-numbered reason. A bad row must
not silently become a valid record. Return valid records and row errors. Corrected input can
be resubmitted. For the initial round, files are small and one user is using it locally.

Interoperability seam, not architecture prescription:
- launch: python3 server.py --port PORT --data-dir DIR
- GET /health -> JSON and HTTP 200
- GET / -> browser UI
- POST /imports with JSON {"csv": "id,name,email\n...", "request_id": "optional-string"}
- response JSON has id (import ID), status (queued/running/completed/failed)
- GET /imports/ID -> JSON with id,status,records:[{id,name,email}],errors:[{row,message}]
- completion may be synchronous or asynchronous; clients must follow returned ID.
- GET /imports -> JSON {"imports": [...summaries with id,status...]}

Choose the simplest implementation that fulfills the current need. Report architecture choices
and checks. Do not build speculative infrastructure. Later feedback may change the needs.

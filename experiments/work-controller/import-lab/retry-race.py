#!/usr/bin/env python3
"""Extra HTTP retry probe against the two documented local preview ports."""
import concurrent.futures
import json
import sys
import urllib.error
import urllib.request
import uuid
from pathlib import Path

results = []
for arm, port in [('controller', 4341), ('native', 4342)]:
    request_id = 'race-' + uuid.uuid4().hex
    payload = {'csv': 'id,name,email\n1,Ada,ada@example.com\n', 'request_id': request_id}
    def post(body):
        request = urllib.request.Request(f'http://127.0.0.1:{port}/imports', data=json.dumps(body).encode(), headers={'Content-Type': 'application/json'})
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            with error:
                return error.code, json.load(error)
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        replies = list(pool.map(post, [payload] * 16))
    ids = {body.get('id') for status, body in replies}
    assert all(status in (200, 201, 202) for status, _ in replies), replies
    assert len(ids) == 1 and None not in ids, replies
    status, _ = post({**payload, 'csv': 'id,name,email\n1,Changed,changed@example.com\n'})
    assert status == 409, status
    with urllib.request.urlopen(f'http://127.0.0.1:{port}/imports', timeout=5) as response:
        summaries = json.load(response)['imports']
    assert sum(item['id'] in ids for item in summaries) == 1
    results.append({'arm': arm, 'simultaneous_retries': 16, 'distinct_import_ids': 1, 'conflict_status': status, 'passed': True})
Path(sys.argv[1] if len(sys.argv) > 1 else '/tmp/import-retry-race.json').write_text(json.dumps(results, indent=2) + '\n')
print(json.dumps(results, indent=2))

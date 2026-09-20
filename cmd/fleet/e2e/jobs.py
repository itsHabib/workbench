#!/usr/bin/env python3
"""Black-box job service drill. Usage: python3 jobs.py /path/to/fleet [receipt.json].
Disposable local state only. Workers are deterministic processes, not LLMs.
"""
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def call(url, **request):
    req = urllib.request.Request(url + '/v1/jobs', json.dumps(request).encode(),
                                 {'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as error:
        with error:
            return error.code, json.load(error)


def start(binary, directory):
    process = subprocess.Popen([binary, 'job', 'serve', '--state', directory,
                                '--listen', '127.0.0.1:0'], stdout=subprocess.DEVNULL,
                               stderr=subprocess.PIPE, text=True)
    line = process.stderr.readline().strip()
    if not line.startswith('Fleet job API: '):
        process.kill()
        process.wait()
        process.stderr.close()
        raise RuntimeError(line)
    return process, 'http://' + line.split('Fleet job API: ', 1)[1]


def stop(process):
    process.kill()
    process.wait(timeout=10)
    process.stderr.close()


def token(job):
    return job['attempts'][-1]['token']


def worker(url, name):
    status, job = call(url, op='claim', worker=name, key=name, ttl_seconds=30)
    if status != 200:
        raise AssertionError((status, job))
    result = hashlib.sha256(job['brief'].encode()).hexdigest()
    status, reported = call(url, op='complete', id=job['id'], worker=name,
                            token=token(job), result=result)
    assert status == 200, reported
    return reported


def run(binary):
    checks = []
    with tempfile.TemporaryDirectory(prefix='fleet-jobs-drill-') as directory:
        process, url = start(binary, directory)
        try:
            for i in range(2):
                status, _ = call(url, op='submit', id=f'job-{i}', brief=f'Hash specimen {i}')
                assert status == 200
            # Separate OS worker processes compete through the central API.
            with concurrent.futures.ProcessPoolExecutor(max_workers=2) as pool:
                results = list(pool.map(worker, [url, url], ['worker-a', 'worker-b']))
            assert len({job['id'] for job in results}) == 2
            checks.append('two process workers claim distinct jobs and report results')
            # Service dies after reporting, before coordinator acceptance.
            stop(process)
            process, url = start(binary, directory)
            for job in results:
                expected = hashlib.sha256(job['brief'].encode()).hexdigest()
                assert job['attempts'][-1]['result'] == expected
                status, accepted = call(url, op='accept', id=job['id'], token=token(job),
                                        evidence='independently recomputed SHA256 ' + expected)
                assert status == 200 and accepted['state'] == 'accepted', accepted
            checks.append('reported artifacts survive API SIGKILL/restart and independent acceptance')
            call(url, op='submit', id='interrupted', brief='recover a stopped worker')
            # Worker claims then parks. Kill the actual claimant process before completion.
            child = subprocess.Popen([sys.executable, __file__, '--park', url],
                                     stdout=subprocess.PIPE, text=True)
            try:
                old = json.loads(child.stdout.readline())
                child.kill()
                child.wait(timeout=5)
            finally:
                if child.poll() is None:
                    child.kill()
                    child.wait()
                child.stdout.close()
            time.sleep(1.1)
            status, replacement = call(url, op='claim', id='interrupted', worker='replacement',
                                       key='replacement-1', ttl_seconds=30)
            assert status == 200, replacement
            assert token(replacement) != token(old)
            status, _ = call(url, op='complete', id='interrupted', worker='parked',
                             token=token(old), result='late')
            assert status == 409
            status, replay = call(url, op='claim', id='interrupted', worker='replacement',
                                  key='replacement-1', ttl_seconds=30)
            assert status == 200 and token(replay) == token(replacement)
            checks.append('killed claimant expires; replacement claims once; late result rejected')
            status, _ = call(url, op='complete', id='interrupted', worker='replacement',
                             token=token(replacement), result='recovered')
            assert status == 200
            status, _ = call(url, op='retry', id='interrupted', token=token(replacement),
                             evidence='independent reviewer requests repair')
            assert status == 200
            status, repaired = call(url, op='claim', id='interrupted', worker='replacement',
                                   key='repair-1', ttl_seconds=30)
            assert status == 200 and token(repaired) != token(replacement)
            checks.append('review rejection queues a fresh fenced repair attempt')
            status, metrics = call(url, op='metrics')
            assert status == 200 and metrics['states']['accepted'] == 2
            assert metrics['attempts'] == 5 and metrics['retries'] == 2, metrics
            return {'checks': checks, 'metrics': metrics,
                    'limits': ['deterministic workers; no LLM/provider run',
                               'lease fences results, not external effects',
                               'single host; no Rooms or cloud run']}
        finally:
            if process.poll() is None:
                stop(process)


if __name__ == '__main__':
    if sys.argv[1] == '--park':
        status, job = call(sys.argv[2], op='claim', id='interrupted', worker='parked',
                           key='parked-1', ttl_seconds=1)
        assert status == 200, job
        print(json.dumps(job), flush=True)
        time.sleep(60)
        sys.exit(0)
    receipt = run(str(Path(sys.argv[1]).resolve()))
    output = json.dumps(receipt, indent=2) + '\n'
    if len(sys.argv) > 2:
        Path(sys.argv[2]).write_text(output)
    print(output, end='')

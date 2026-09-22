#!/usr/bin/env python3
"""Deterministic failure drill. No model calls or worker processes are launched."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile

root = Path(__file__).resolve().parent
records = []


def run(db, *args, ok=True):
    proc = subprocess.run([sys.executable, str(root / 'controller.py'), '--db', str(db), *args],
                          capture_output=True, text=True, check=False)
    result = json.loads(proc.stdout)
    records.append({'args': list(args), 'exit': proc.returncode, 'result': result})
    assert (proc.returncode == 0) == ok, records[-1]
    return result


def drill(db):
    run(db, 'init')
    run(db, 'register', 'builder', 'Owns parser implementation')
    run(db, 'register', 'replacement', 'Recovery worker in a separate workspace')
    run(db, 'submit', 'parser', 'Build the parser')
    run(db, 'submit', 'parser', 'Build the parser')
    run(db, 'submit', 'parser', 'Conflicting request', ok=False)
    run(db, 'decide', 'parser', '--worker', 'builder', '--reason', 'Reuse parser context')
    old = str(run(db, 'claim', 'parser', '--worker', 'builder')['token'])
    run(db, 'submit', 'errors', 'Improve parser errors')
    run(db, 'decide', 'errors', '--worker', 'builder', '--reason', 'Related work; queue behind parser')
    run(db, 'claim', 'errors', '--worker', 'builder', ok=False)
    run(db, 'submit', 'guide', 'Write independent usage guide')
    run(db, 'decide', 'guide', '--worker', 'writer', '--spawn', '--reason', 'Independent documentation can proceed')
    # Every CLI call is a new process: persisted state is the only continuity.
    state = run(db, 'snapshot')
    assert len(state['jobs']) == 3
    intents = run(db, 'outbox')
    assert sum(e['kind'] == 'spawn' for e in intents) == 1
    run(db, 'accept', 'parser', '--token', old, '--evidence', 'Premature acceptance', ok=False)
    run(db, 'reassign', 'parser', '--worker', 'replacement', '--reason', 'Simulated confirmed worker loss')
    token = str(run(db, 'claim', 'parser', '--worker', 'replacement')['token'])
    assert int(token) > int(old)
    run(db, 'complete', 'parser', '--worker', 'builder', '--token', old, '--result', 'Late old result', ok=False)
    run(db, 'complete', 'parser', '--worker', 'replacement', '--token', token, '--result', 'fixture result')
    run(db, 'complete', 'parser', '--worker', 'replacement', '--token', token, '--result', 'fixture result')
    run(db, 'accept', 'parser', '--token', old, '--evidence', 'Stale acceptance', ok=False)
    run(db, 'accept', 'parser', '--token', token, '--evidence', 'Fixture inspected; not real software verification')
    event = str(intents[0]['id'])
    run(db, 'delivered', event)
    run(db, 'delivered', event)
    run(db, 'snapshot')


if __name__ == '__main__':
    with tempfile.TemporaryDirectory(prefix='controller-drill-') as directory:
        drill(Path(directory) / 'jobs.db')
    output = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(tempfile.mkdtemp(prefix='controller-receipt-')) / 'drill.json'
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps({'kind': 'deterministic-mechanism-drill', 'passed': True,
                                  'records': records}, indent=2) + '\n')
    print(f'PASS: {len(records)} CLI calls; receipt: {output}')

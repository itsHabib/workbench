"""Feed actual colony evidence to the existing Passage CLI, then test stale input.

This records deterministic acceptance judgments, not an independent human review,
merge approval, or automatic deployment. Passage is supplied as a pinned binary.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

from broker import write
from lab import read
from workload import digest


def check_inputs(run):
    selected = read(run / 'artifacts/selected.json')
    qualification = read(run / 'artifacts/qualification.json')
    audit = read(run / 'host-audit.json')
    if digest(selected['source']) != selected['sha256'] or selected['sha256'] != qualification['sha256']:
        raise ValueError('qualification source mismatch')
    if qualification.get('valid') is not True or qualification.get('qualified') is not True:
        raise ValueError('candidate did not qualify')
    if audit.get('passed') is not True or audit.get('qualification') != qualification:
        raise ValueError('host recovery audit is missing or refers to another result')
    checks = audit.get('checks', {})
    if not checks or not all(value is True for value in checks.values()):
        raise ValueError('host recovery audit contains missing or failed checks')
    return selected, qualification, audit


def run(binary, source, out):
    selected, qualification, audit = check_inputs(source)
    out.mkdir(mode=0o700)
    subject = out / 'subject'
    subject.mkdir()
    (subject / 'candidate.py').write_text(selected['source'])
    write(subject / 'qualification.json', qualification)
    write(subject / 'host-audit.json', audit)
    contract = {'name': 'Room colony qualification and recovery', 'phases': [
        {'name': 'verification', 'brief': 'Accept the exact generated source on held-out maps.',
         'inputs': ['candidate.py', 'qualification.json'], 'git': False, 'requires': ['qualified']},
        {'name': 'recovery', 'brief': 'Accept completion after loss of the Linux host kernel.',
         'inputs': ['candidate.py', 'qualification.json', 'host-audit.json'], 'git': False,
         'requires': ['host-recovered']}]}
    write(out / 'contract.json', contract)
    commands = []

    def call(*args, expected=0):
        result = subprocess.run([str(binary), *map(str, args)], capture_output=True, text=True, timeout=30)
        commands.append({'args': list(map(str, args)), 'exit': result.returncode,
                         'stdout': result.stdout, 'stderr': result.stderr})
        write(out / 'commands.json', commands)
        if result.returncode != expected:
            raise ValueError(f'Passage command returned {result.returncode}, expected {expected}')
        return result.stdout

    work = out / 'work.json'
    call('init', '--work', work, '--root', subject, '--contract', out / 'contract.json')
    call('check', '--work', work, expected=1)

    def mutate(action, *args):
        revision = len(read(work)['events'])
        return call(action, '--work', work, '--expect', revision, '--by', 'colony-evidence-check',
                    '--note', 'Deterministic acceptance of retained experiment evidence; not merge authority', *args)

    for requirement, evidence in [('qualified', 'qualification.json'), ('host-recovered', 'host-audit.json')]:
        mutate('record', '--requirement', requirement, '--verdict', 'pass', '--evidence', subject / evidence)
        mutate('advance')
    complete = json.loads(call('status', '--work', work))
    if complete['phase'] != 'complete' or not complete['ready']:
        raise ValueError('Passage did not accept the two evidence handoffs')
    (subject / 'candidate.py').write_text(selected['source'] + '\n# changed after qualification\n')
    call('check', '--work', work, expected=1)
    stale = json.loads(call('status', '--work', work))
    if stale['ready']:
        raise ValueError('Passage accepted stale source')
    (subject / 'candidate.py').write_text(selected['source'])
    call('check', '--work', work)
    report = {'complete': complete, 'changed_source': stale,
              'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
              'judgment': 'deterministic adapter over retained evidence; no merge or deployment'}
    write(out / 'summary.json', report)
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--run', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    print(json.dumps(run(args.binary.resolve(), args.run.resolve(), args.out.resolve()), indent=2))

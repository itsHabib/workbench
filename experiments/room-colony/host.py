"""Linux-owned colony replay. No Mac broker, credentials, or persistent controller.

Run under a dedicated Linux host account with permission to launch Rooms.
Power-loss recovery requires the SAME persistent disk and a changed Linux boot ID.
Old incomplete attempts remain evidence of loss, never fabricated clean exits.
"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import signal
import subprocess
import sys
import time

from broker import Handler, Store, ThreadingHTTPServer, sync_directory, write
from lab import HERE, ROLES, alive, payload, read, source_hash


def boot_id():
    return Path('/proc/sys/kernel/random/boot_id').read_text().strip()


def fingerprint():
    return hashlib.sha256((source_hash() + (HERE / 'host.py').read_text()).encode()).hexdigest()


def runtime_home(root, current_boot):
    home = root / 'boots' / current_boot[:8] / 'h'
    socket = str(home / ('.local/state/rooms/jailer/firecracker/' + 'x' * 26 + '/root/api.sock'))
    if len(socket.encode()) > 107:
        raise ValueError('run path too long for Linux socket; use a short root such as /opt/c-a')
    record = home.parent / 'observation.json'
    if record.exists() and read(record)['boot'] != current_boot:
        raise ValueError('short boot directory collision; preserve evidence')
    return home


def backend(config):
    paths = [config['rooms'], config['image'], config['toolstore'] + '/toolstore.sqfs',
             str(Path(config['image']).parent / 'vmlinux.bin')]
    return subprocess.check_output(['sha256sum', *paths], text=True).splitlines()


def events(attempt):
    path = attempt / 'lifecycle.ndjson'
    if not path.exists():
        return []
    lines, result = path.read_text().splitlines(), []
    for index, line in enumerate(lines):
        try:
            result.append(json.loads(line)['event'])
        except json.JSONDecodeError:
            if index != len(lines) - 1:
                raise
            # A power cut can truncate the final append. Keep the original file;
            # partial bytes never count as a completed lifecycle event.
    return result


def copy_key(source, target):
    private_file(source.read_bytes(), target)


def private_file(data, target):
    temporary = target.with_suffix('.tmp')
    with temporary.open('wb') as stream:
        os.fchmod(stream.fileno(), 0o600)
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, target)
    sync_directory(target.parent)


def directory(path):
    if path.is_dir():
        return
    directory(path.parent)
    path.mkdir(mode=0o700)
    sync_directory(path.parent)


def clean(attempt):
    return (attempt / 'terminal.json').exists() and all(
        key in events(attempt) for key in ('collection_done', 'cleanup_done'))


def observe(root, current_boot):
    actions = []
    for role in ROLES:
        attempts = sorted((root / 'attempts').glob(role + '-*'))
        records = [(a, read(a / 'launch.json') if (a / 'launch.json').exists() else {}) for a in attempts]
        live = [a for a, record in records if record.get('boot') == current_boot
                and record.get('pid') and alive(record['pid'], str(a))]
        if live:
            actions.append({'role': role, 'action': 'keep'})
            continue
        ambiguous = [a for a, record in records if not record or
                     (record.get('boot') == current_boot and not clean(a))]
        if ambiguous:
            actions.append({'role': role, 'action': 'blocked', 'reason': 'same-boot or unknown launch lacks cleanup evidence'})
            continue
        successful = any(clean(a) and read(a / 'terminal.json')['exit'] == 0 for a in attempts)
        if successful and (root / 'artifacts' / (role + '-done.json')).exists():
            actions.append({'role': role, 'action': 'complete'})
            continue
        actions.append({'role': role, 'action': 'start' if len(attempts) < 4 else 'blocked',
                        'attempt': len(attempts) + 1})
    return actions


class ReplayStore(Store):
    def generate(self, number, value):
        path = self.root / 'model' / number
        if number not in ('1', '2') or not (path / 'reply.json').exists():
            raise ValueError('replay requires a saved proposal; inference is disabled')
        if read(path / 'request.json') != value:
            raise ValueError('saved proposal input differs from this request')
        result = read(path / 'reply.json')
        self.put('replayed-' + number, {'sha256': result['sha256']})
        return result


def serve(root):
    server = ThreadingHTTPServer(('0.0.0.0', 0), Handler)
    server.token = (root / 'token').read_text().strip()
    server.store = ReplayStore(root)
    write(root / 'broker.json', {'pid': os.getpid(), 'port': server.server_port, 'boot': boot_id()})
    server.serve_forever()


def launch_broker(root, current_boot):
    path = root / 'broker.json'
    if path.exists():
        old = read(path)
        if old['boot'] == current_boot and alive(old['pid'], str(root)):
            return
    with (root / ('broker-' + current_boot + '.log')).open('a') as log:
        subprocess.Popen([sys.executable, str(HERE / 'host.py'), 'serve', str(root)],
                         stdout=log, stderr=log, stdin=subprocess.DEVNULL, start_new_session=True)
    for _ in range(100):
        if path.exists() and read(path)['boot'] == current_boot:
            record = read(path)
            if alive(record['pid'], str(root)):
                return
        time.sleep(.1)
    raise TimeoutError('broker startup')


def initialize(args):
    root = args.root
    runtime_home(root, boot_id())
    if root.exists():
        raise FileExistsError(root)
    directory(root)
    for name in ('artifacts', 'attempts', 'model', 'boots'):
        directory(root / name)
    config = {'source': fingerprint(), 'rooms': str(args.rooms.resolve()),
              'image': str(args.image.resolve()), 'toolstore': str(args.toolstore.resolve())}
    config['backend'] = backend(config)
    write(root / 'config.json', config)
    private_file(secrets.token_hex(24).encode(), root / 'token')
    copy_key(args.key, root / 'id_rooms')
    for number in ('1', '2'):
        target = root / 'model' / number
        directory(target)
        for name in ('request.json', 'reply.json'):
            write(target / name, read(args.replay / number / name))


def apply(root):
    with (root / 'apply.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        config, current_boot = read(root / 'config.json'), boot_id()
        if config['source'] != fingerprint() or config['backend'] != backend(config):
            raise ValueError('source/backend drift; preserve run and initialize a new one')
        actions = observe(root, current_boot)
        home = runtime_home(root, current_boot)
        directory(home / '.ssh')
        copy_key(root / 'id_rooms', home / '.ssh/id_rooms')
        # A new kernel means the previous kernel's VMMs cannot survive. Preserve
        # old Room registries/disks instead of letting reused PIDs appear live.
        write(home.parent / 'observation.json', {'boot': current_boot, 'actions': actions})
        launch_broker(root, current_boot)
        for action in actions:
            if action['action'] != 'start':
                continue
            attempt = root / 'attempts' / f"{action['role']}-{action['attempt']}"
            directory(attempt)
            write(attempt / 'launch.json', {'boot': current_boot, 'pid': None})
            with (attempt / 'wrapper.log').open('w') as log:
                process = subprocess.Popen([sys.executable, str(HERE / 'host.py'), 'attempt', str(root),
                                            '--role', action['role'], '--attempt', str(attempt)],
                                           stdout=log, stderr=log, stdin=subprocess.DEVNULL, start_new_session=True)
            write(attempt / 'launch.json', {'boot': current_boot, 'pid': process.pid})
        return actions


def attempt_run(args):
    root, attempt = args.root, args.attempt
    config, broker = read(root / 'config.json'), read(root / 'broker.json')
    gateway = "ip route | awk '$1 == \"default\" {print $3; exit}'"
    command = ('set -eu; mkdir -p /tmp/colony; printf %s ' + shlex.quote(payload())
               + ' | base64 -d | tar -xz -C /tmp/colony; cd /tmp/colony; exec python3 worker.py '
               + args.role + f' "http://$({gateway}):{broker["port"]}" '
               + shlex.quote((root / 'token').read_text().strip()))
    home = runtime_home(root, boot_id())
    argv = [config['rooms'], 'run', '--image', config['image'], '--toolstore', config['toolstore'],
            '--cpus', '1', '--memory', '512', '--disk', '1', '--command', command,
            '--max-wall', '600s', '--out', str(attempt / 'out'),
            '--lifecycle', str(attempt / 'lifecycle.ndjson'), '--json']
    with (attempt / 'stdout.txt').open('w') as out, (attempt / 'stderr.txt').open('w') as err:
        result = subprocess.run(argv, env={**os.environ, 'HOME': str(home)}, stdout=out, stderr=err)
    write(attempt / 'terminal.json', {'exit': result.returncode, 'boot': boot_id()})


def audit(root):
    rows = []
    for path in sorted((root / 'attempts').iterdir()):
        rows.append({'attempt': path.name, 'launch': read(path / 'launch.json'),
                     'terminal': read(path / 'terminal.json') if (path / 'terminal.json').exists() else None,
                     'events': events(path)})
    artifacts = {p.stem: read(p) for p in (root / 'artifacts').glob('*.json')}
    selected, result, promotion = [artifacts.get(k, {}) for k in ('selected', 'qualification', 'promotion')]
    before = read(root / 'before-power-loss.json') if (root / 'before-power-loss.json').exists() else {}
    after = read(root / 'after-power-loss.json') if (root / 'after-power-loss.json').exists() else {}
    from workload import digest
    checks = {
        'multiple_host_boots': len({r['launch']['boot'] for r in rows}) >= 2,
        'observed_host_loss': bool(before) and before.get('boot') != after.get('boot') == boot_id()
            and before.get('broker_alive') is True,
        'submission_survived': bool(before) and before.get('candidate_one_sha256') == artifacts.get('candidate-1', {}).get('sha256'),
        'incomplete_old_boot_preserved': any(r['launch']['boot'] != boot_id() and r['terminal'] is None for r in rows),
        'roles_have_collected_success': all(any(r['attempt'].startswith(role + '-')
            and r['terminal'] and r['terminal']['exit'] == 0
            and all(e in r['events'] for e in ('collection_done', 'cleanup_done')) for r in rows) for role in ROLES),
        'new_boot_worker_succeeded': any(r['launch']['boot'] == boot_id() and r['terminal']
            and r['terminal']['exit'] == 0 and all(e in r['events'] for e in ('collection_done', 'cleanup_done')) for r in rows),
        'planner_agrees_complete': all(a['action'] == 'complete' for a in observe(root, boot_id())),
        'both_roles_done': all(role + '-done' in artifacts for role in ROLES),
        'source_bound': bool(selected) and digest(selected.get('source', '')) == selected.get('sha256') == result.get('sha256') == promotion.get('sha256'),
        'receipt_bound': bool(result) and digest(json.dumps(result, sort_keys=True)) == promotion.get('qualification_sha256'),
        'qualified': result.get('qualified') is True and promotion.get('promoted') is True,
        'negative_control': artifacts.get('negative-control', {}).get('valid') is False,
        'two_saved_proposals': all('replayed-' + i in artifacts for i in ('1', '2')),
        'one_injected_author_exit': len([r for r in rows if r['attempt'].startswith('author-') and r['terminal'] and r['terminal']['exit'] == 137]) == 1,
    }
    report = {'passed': all(checks.values()), 'checks': checks, 'attempts': rows,
              'qualification': result, 'inference': 'saved proposals only; no fresh model calls',
              'source': read(root / 'config.json')['source']}
    write(root / 'host-audit.json', report)
    return report


def stop(root):
    with (root / 'apply.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        actions = observe(root, boot_id())
        if any(a['action'] in ('keep', 'blocked') for a in actions):
            raise ValueError('workers live or ambiguous; retain broker and evidence')
        config = read(root / 'config.json')
        inventory = json.loads(subprocess.check_output([config['rooms'], 'ls', '--json'],
            env={**os.environ, 'HOME': str(runtime_home(root, boot_id()))}, text=True))
        write(root / 'final-rooms.json', inventory)
        if inventory.get('rooms'):
            raise ValueError('current-boot Rooms remain; preserve broker and evidence')
        path = root / 'broker.json'
        if path.exists():
            record = read(path)
            if record['boot'] == boot_id() and alive(record['pid'], str(root)):
                os.kill(record['pid'], signal.SIGTERM)
        # Do not delete any run data. Old-boot overlays and partial logs matter.


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('verb', choices=('init', 'plan', 'apply', 'serve', 'attempt', 'audit', 'stop'))
    parser.add_argument('root', type=Path)
    for flag in ('rooms', 'image', 'toolstore', 'key', 'replay', 'attempt'):
        parser.add_argument('--' + flag, type=Path)
    parser.add_argument('--role', choices=ROLES)
    args = parser.parse_args()
    args.root = args.root.resolve()
    if args.verb == 'init':
        initialize(args)
        return
    if args.verb == 'attempt':
        attempt_run(args)
        return
    if args.verb == 'plan':
        print(json.dumps(observe(args.root, boot_id()), indent=2))
        return
    result = {'apply': apply, 'serve': serve, 'audit': audit, 'stop': stop}[args.verb](args.root)
    if result is not None:
        print(json.dumps(result, indent=2))
    if args.verb == 'audit' and not result['passed']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()

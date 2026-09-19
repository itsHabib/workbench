#!/usr/bin/env python3
"""Local job lifecycle experiment. Routing judgment and transport are external."""
import argparse
import json
import sqlite3
import sys
from contextlib import closing, contextmanager

SCHEMA = """
CREATE TABLE IF NOT EXISTS workers (id TEXT PRIMARY KEY, context TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS jobs (
 id TEXT PRIMARY KEY, text TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'queued',
 worker TEXT REFERENCES workers(id), token INTEGER, result TEXT, evidence TEXT);
CREATE UNIQUE INDEX IF NOT EXISTS one_running_per_worker ON jobs(worker)
 WHERE state = 'running';
CREATE TABLE IF NOT EXISTS attempts (token INTEGER PRIMARY KEY AUTOINCREMENT, job TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS decisions (
 id INTEGER PRIMARY KEY, job TEXT NOT NULL, worker TEXT NOT NULL,
 reason TEXT NOT NULL, kind TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, worker TEXT,
 job TEXT, payload TEXT NOT NULL, delivered INTEGER NOT NULL DEFAULT 0);
"""


class Rejected(ValueError):
    pass


@contextmanager
def transaction(path):
    db = sqlite3.connect(path, timeout=10, isolation_level=None)
    db.row_factory = sqlite3.Row
    db.execute('PRAGMA foreign_keys = ON')
    try:
        db.execute('BEGIN IMMEDIATE')
        yield db
        db.commit()
    except Exception:
        db.rollback()
        raise
    finally:
        db.close()


def required(text):
    if not text.strip():
        raise Rejected('values must not be empty')
    return text


def job_row(db, job):
    row = db.execute('SELECT * FROM jobs WHERE id = ?', (job,)).fetchone()
    if row is None:
        raise Rejected('unknown job: ' + job)
    return dict(row)


def event(db, kind, worker, job, **payload):
    db.execute('INSERT INTO events(kind, worker, job, payload) VALUES (?, ?, ?, ?)',
               (kind, worker, job, json.dumps(payload, sort_keys=True)))


def register(db, worker, context):
    required(worker)
    required(context)
    row = db.execute('SELECT context FROM workers WHERE id = ?', (worker,)).fetchone()
    if row is not None and row['context'] != context:
        raise Rejected('worker already registered with different context')
    db.execute('INSERT OR IGNORE INTO workers VALUES (?, ?)', (worker, context))
    return {'worker': worker, 'context': context}


def route(db, args):
    job = job_row(db, args.job)
    allowed = ('assigned', 'running', 'reported')
    if args.command == 'decide':
        allowed = ('queued',)
    if job['state'] not in allowed:
        raise Rejected('cannot ' + args.command + ' a ' + job['state'] + ' job')
    required(args.reason)
    worker = db.execute('SELECT id FROM workers WHERE id = ?', (args.worker,)).fetchone()
    if getattr(args, 'spawn', False):
        if worker is not None:
            raise Rejected('spawn intent requires a new worker identity')
        register(db, args.worker, job['text'])
        event(db, 'spawn', args.worker, args.job, context=job['text'])
    if worker is None and not getattr(args, 'spawn', False):
        raise Rejected('unknown worker: ' + args.worker)
    db.execute('UPDATE jobs SET state = ?, worker = ?, token = NULL, result = NULL, evidence = NULL WHERE id = ?',
               ('assigned', args.worker, args.job))
    db.execute('INSERT INTO decisions(job, worker, reason, kind) VALUES (?, ?, ?, ?)',
               (args.job, args.worker, args.reason, args.command))
    event(db, 'assignment', args.worker, args.job, text=job['text'], reason=args.reason)
    return job_row(db, args.job)


def claim(db, args):
    job = job_row(db, args.job)
    if job['state'] != 'assigned' or job['worker'] != args.worker:
        raise Rejected('job is not assigned to this worker or already claimed')
    busy = db.execute("SELECT id FROM jobs WHERE worker = ? AND state = 'running'", (args.worker,)).fetchone()
    if busy is not None:
        raise Rejected('worker is already running job: ' + busy['id'])
    token = db.execute('INSERT INTO attempts(job) VALUES (?)', (args.job,)).lastrowid
    db.execute("UPDATE jobs SET state = 'running', token = ? WHERE id = ?", (token, args.job))
    return job_row(db, args.job)


def complete(db, args):
    job = job_row(db, args.job)
    required(args.result)
    if job['worker'] != args.worker or job['token'] != args.token:
        raise Rejected('stale attempt or wrong worker')
    if job['state'] in ('reported', 'accepted') and job['result'] == args.result:
        return job
    if job['state'] != 'running':
        raise Rejected('attempt is not running, or result conflicts with prior report')
    db.execute("UPDATE jobs SET state = 'reported', result = ? WHERE id = ?", (args.result, args.job))
    event(db, 'reported', None, args.job, token=args.token, result=args.result)
    return job_row(db, args.job)


def accept(db, args):
    job = job_row(db, args.job)
    required(args.evidence)
    if job['token'] != args.token:
        raise Rejected('stale attempt')
    if job['state'] == 'accepted' and job['evidence'] == args.evidence:
        return job
    if job['state'] != 'reported':
        raise Rejected('only a current reported result can be accepted')
    db.execute("UPDATE jobs SET state = 'accepted', evidence = ? WHERE id = ?", (args.evidence, args.job))
    return job_row(db, args.job)


def rows(db, table):
    return [dict(row) for row in db.execute('SELECT * FROM ' + table + ' ORDER BY id')]


def execute(args):
    if args.command == 'init':
        with closing(sqlite3.connect(args.db)) as db:
            db.executescript(SCHEMA)
        return {'initialized': True}
    with transaction(args.db) as db:
        if args.command == 'register':
            return register(db, args.worker, args.context)
        if args.command == 'submit':
            required(args.job)
            required(args.text)
            prior = db.execute('SELECT text FROM jobs WHERE id = ?', (args.job,)).fetchone()
            if prior is not None and prior['text'] != args.text:
                raise Rejected('job ID already has different text')
            db.execute('INSERT OR IGNORE INTO jobs(id, text) VALUES (?, ?)', (args.job, args.text))
            return job_row(db, args.job)
        if args.command in ('decide', 'reassign'):
            return route(db, args)
        if args.command == 'claim':
            return claim(db, args)
        if args.command == 'complete':
            return complete(db, args)
        if args.command == 'accept':
            return accept(db, args)
        if args.command == 'snapshot':
            return {table: rows(db, table) for table in ('jobs', 'workers', 'decisions')}
        if args.command == 'outbox':
            result = [dict(row) for row in db.execute('SELECT * FROM events WHERE delivered = 0 ORDER BY id')]
            for item in result:
                item['payload'] = json.loads(item['payload'])
            return result
        if args.command == 'delivered':
            changed = db.execute('UPDATE events SET delivered = 1 WHERE id = ?', (args.event_id,)).rowcount
            if not changed:
                raise Rejected('unknown event')
            return {'delivered': args.event_id}
    raise Rejected('unknown command')


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--db', required=True)
    commands = p.add_subparsers(dest='command', required=True)
    for name in ('init', 'snapshot', 'outbox'):
        commands.add_parser(name)
    sub = commands.add_parser('submit')
    sub.add_argument('job')
    sub.add_argument('text')
    sub = commands.add_parser('register')
    sub.add_argument('worker')
    sub.add_argument('context')
    for name in ('decide', 'reassign', 'claim', 'complete'):
        sub = commands.add_parser(name)
        sub.add_argument('job')
        sub.add_argument('--worker', required=True)
        if name in ('decide', 'reassign'):
            sub.add_argument('--reason', required=True)
        if name == 'decide':
            sub.add_argument('--spawn', action='store_true')
        if name == 'complete':
            sub.add_argument('--token', required=True, type=int)
            sub.add_argument('--result', required=True)
    sub = commands.add_parser('accept')
    sub.add_argument('job')
    sub.add_argument('--token', required=True, type=int)
    sub.add_argument('--evidence', required=True)
    sub = commands.add_parser('delivered')
    sub.add_argument('event_id', type=int)
    return p


def main():
    try:
        print(json.dumps(execute(parser().parse_args()), sort_keys=True))
    except (Rejected, sqlite3.Error) as exc:
        print(json.dumps({'error': str(exc)}))
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())

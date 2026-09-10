#!/usr/bin/env python3
"""Scorecard for one Fleet e2e run, from records only.

Usage:
  run-metrics.py <run-id> <since-iso-utc> --repo <owner>/<repo> \
      --roles supervisor:x,supervisor:x-a,supervisor:x-b --seats <repo>-author-1,<repo>-author-2,<repo>-verifier-1 \
      [--until <iso-utc>] [--tenant mh] [--poll-log <path>] [--out runs/<run-id>.json]

Reads: the v2 mail store ($FLEET_STATE/mail/.v2), receipts, the org chains for --roles,
watch/observed.jsonl (and an optional poller log) for launches, and the Claude transcripts of
every session that ran in a directory named after the repo. Nothing here trusts a session's
own summary; every number comes from a file.
"""
import argparse
import calendar
import collections
import glob
import hashlib
import json
import os
import re
import time

FLEET = os.path.expanduser(os.environ.get("FLEET_STATE", "~/.fleet"))
ORG = os.path.expanduser(os.environ.get("ORG_STATE", "~/dev/org/state"))
PROJ = os.path.expanduser("~/.claude/projects")


def ts(s):
    return calendar.timegm(time.strptime(s.replace("Z", ""), "%Y-%m-%dT%H:%M:%S"))


def mail(tenant, addresses, since, until):
    """Every mail record for the given addresses in the window, oldest first."""
    out = []
    troot = os.path.join(FLEET, "mail", ".v2", hashlib.sha256(tenant.encode()).hexdigest())
    for a in addresses:
        kind = "role" if ":" in a else "seat"
        d = os.path.join(troot, kind, hashlib.sha256(a.encode()).hexdigest())
        for p in glob.glob(os.path.join(d, "*.json")):
            try:
                m = json.load(open(p))
            except Exception:
                continue
            if since <= m.get("at", 0) <= until:
                out.append(m)
    return sorted(out, key=lambda m: m["at"])


def level(addr, roles, overall):
    if addr == overall:
        return "overall"
    if addr in roles:
        return "lead"
    return "seat"


def relays(mails):
    base = lambda i: re.sub(r"-(order|relay|answer|reply)$", "", i)
    by = collections.defaultdict(list)
    for m in mails:
        by[base(m["id"])].append(m)
    out = {}
    for k, v in by.items():
        if len(v) < 2:
            continue
        path = [v[0].get("from_address", v[0].get("from_role"))] + [m["to"] for m in v]
        out[k] = {"depth": len(v), "path": path, "kinds": [m["kind"] for m in v],
                  "hop_seconds": [round(v[i + 1]["at"] - v[i]["at"]) for i in range(len(v) - 1)],
                  "total_seconds": round(v[-1]["at"] - v[0]["at"])}
    return out


def chains(tenant, roles, since, until):
    out = {}
    for r in roles:
        p = os.path.join(ORG, tenant, r.replace(":", "--"), "chain.jsonl")
        if not os.path.exists(p):
            continue
        c = collections.Counter()
        for l in open(p):
            rec = json.loads(l)
            if since <= ts(rec["at"]) <= until:
                c[rec["kind"]] += 1
        out[r] = dict(c)
    return out


def receipts(since, until):
    out = []
    for p in glob.glob(os.path.join(FLEET, "receipts", "*.json")):
        try:
            r = json.load(open(p))
        except Exception:
            continue
        if since <= r.get("at", 0) <= until:
            out.append({"sha": r["sha"][:7], "kind": r["kind"], "verdict": r["verdict"], "session": r["session"][:8]})
    return sorted(out, key=lambda x: x["sha"])


def launches(mails, since, until, poll_log):
    """Watcher launches (observed.jsonl) plus poller launches, with mail-to-launch latency."""
    events = []
    obs = os.path.join(FLEET, "watch", "observed.jsonl")
    if os.path.exists(obs):
        for l in open(obs):
            if '"mail-delivery-started"' not in l:
                continue
            r = json.loads(l)
            if since <= r["at"] <= until:
                events.append((r["at"], r["role"], r.get("ids", [])))
    if poll_log and os.path.exists(poll_log):
        for l in open(poll_log):
            if " launch " not in l:
                continue
            t = ts(l[:19])
            if since <= t <= until:
                addr = l.split(" launch ")[1].split(" ids:")[0]
                events.append((t, addr, l.split("ids:")[1].split()))
    lat = []
    for t, addr, ids in events:
        for m in mails:
            if m["id"] in ids and m["to"] == addr:
                lat.append(round(t - m["at"]))
    lat.sort()
    return {"count": len(events), "by_address": dict(collections.Counter(e[1] for e in events)),
            "mail_to_launch_seconds": {"median": lat[len(lat) // 2] if lat else None,
                                       "min": lat[0] if lat else None, "max": lat[-1] if lat else None}}


def sessions(repo_base, since, until):
    out = {}
    for f in glob.glob(os.path.join(PROJ, f"*{repo_base}*", "*.jsonl")):
        if not (since <= os.path.getmtime(f) + 3600 and os.path.getmtime(f) >= since):
            continue
        sid = os.path.basename(f)[:8]
        d = f.split("/")[-2]
        d = d[d.find(repo_base):]
        out_tok = turns = 0
        first = None
        for l in open(f):
            try:
                r = json.loads(l)
            except Exception:
                continue
            t = r.get("timestamp")
            if not t:
                continue
            at = ts(t[:19])
            if not (since <= at <= until):
                continue
            first = first or t
            m = r.get("message", {})
            if m.get("role") == "assistant":
                out_tok += (m.get("usage") or {}).get("output_tokens", 0)
                if isinstance(m.get("content"), list) and any(b.get("type") == "text" for b in m["content"]):
                    turns += 1
        if first:
            out[sid] = {"dir": d, "turns": turns, "out_tokens": out_tok, "first": first[11:19]}
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("run")
    ap.add_argument("since")
    ap.add_argument("--until")
    ap.add_argument("--repo", required=True)
    ap.add_argument("--roles", required=True, help="overall lead first, then bucket leads, comma separated")
    ap.add_argument("--seats", required=True, help="seat addresses, comma separated")
    ap.add_argument("--tenant", default=os.environ.get("ORG_TENANT", "mh"))
    ap.add_argument("--poll-log")
    ap.add_argument("--out")
    a = ap.parse_args()
    since = ts(a.since)
    until = ts(a.until) if a.until else time.time()
    roles = a.roles.split(",")
    seats = a.seats.split(",")
    ms = mail(a.tenant, roles + seats, since, until)
    overall = roles[0]
    edges = collections.Counter(f"{level(m.get('from_address', m.get('from_role')), roles, overall)}->{level(m['to'], roles, overall)}" for m in ms)
    repo_base = a.repo.split("/")[-1]
    sess = sessions(repo_base, since, until)
    card = {
        "run": a.run, "repo": a.repo, "since": a.since,
        "until": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(until)),
        "mail": {"count": len(ms), "kinds": dict(collections.Counter(m["kind"] for m in ms)),
                 "edges": dict(edges), "acked": sum(1 for m in ms if m.get("acked_at"))},
        "relays": relays(ms), "chains": chains(a.tenant, roles, since, until),
        "receipts": receipts(since, until), "launches": launches(ms, since, until, a.poll_log),
        "sessions": {"count": len(sess), "out_tokens": sum(s["out_tokens"] for s in sess.values()),
                     "by_session": sess},
    }
    out = a.out or f"runs/{a.run}.json"
    os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
    json.dump(card, open(out, "w"), indent=2)
    m = card["mail"]
    print(f"run {a.run}: mail {m['count']} {m['kinds']} edges {m['edges']}; receipts {len(card['receipts'])}; "
          f"launches {card['launches']['count']} latency {card['launches']['mail_to_launch_seconds']}; "
          f"sessions {card['sessions']['count']} out tokens {card['sessions']['out_tokens']}")
    for k, v in sorted(card["relays"].items(), key=lambda kv: -kv[1]["depth"]):
        print(f"  relay {k}: depth {v['depth']} {' -> '.join(v['path'])} hops {v['hop_seconds']} total {v['total_seconds']}s")


if __name__ == "__main__":
    main()

#!/usr/bin/env bash
# Stand-in for the deferred Fleet launcher (workbench #297 D5): every POLL seconds, for each
# address in CONFIG, if it has unacked mail not yet launched and no live session in its
# directory, start ONE session with all those mail lines in the prompt. Reads the v2 mail store
# directly (mail/.v2/<sha256 tenant>/<role|seat>/<sha256 address>/<id>.json), because #298's
# `fleet mail` needs a live roled session and a launcher has none. Records launches in LOG and a
# launched-id set in STATE so no id is launched twice.
# Usage: mail-poll.sh <config.json> <fleet-binary> <state-dir> [poll-seconds] [max-minutes] [tenant]
set -u
CONFIG="$1"; F="$2"; ST="$3"; POLL="${4:-10}"; MAXMIN="${5:-45}"; TENANT="${6:-mh}"
mkdir -p "$ST"
LOG="$ST/poll.log"; LAUNCHED="$ST/launched.txt"; touch "$LAUNCHED"
end=$(( $(date +%s) + MAXMIN*60 ))
while [ "$(date +%s)" -lt "$end" ]; do
  python3 - "$CONFIG" "$TENANT" "$LAUNCHED" "$ST" <<'EOF'
import json,glob,os,sys,time,hashlib,subprocess
cfgpath,tenant,launched_path,st=sys.argv[1:5]
cfg=json.load(open(cfgpath))
launched=set(l.strip() for l in open(launched_path))
root=os.path.expanduser("~/.fleet/mail/.v2")
now=time.time()
def live(cwd):
    for f in glob.glob(os.path.expanduser("~/.fleet/sessions/*.json")):
        try: r=json.load(open(f))
        except Exception: continue
        if r.get("cwd")==cwd and not r.get("ended") and now-os.path.getmtime(f)<1200: return True
    return False
for addr,c in cfg.items():
    kind="role" if ":" in addr else "seat"
    d=os.path.join(root, hashlib.sha256(tenant.encode()).hexdigest(), kind, hashlib.sha256(addr.encode()).hexdigest())
    recs=[]
    for p in glob.glob(os.path.join(d,"*.json")):
        try: m=json.load(open(p))
        except Exception: continue
        if not m.get("acked_at") and f"{addr} {m['id']}" not in launched: recs.append(m)
    if not recs: continue
    if live(c["cwd"]): continue
    recs.sort(key=lambda m:m["at"])
    lines="\n".join(f"[fleet] mail {m['id']} from {m.get('from_address', m.get('from_role'))} ({m['kind']}): {(m.get('subject') or '')[:200]}" for m in recs[:8])
    prompt=f"Mail for {addr}. Run the Fleet binary's `mail --unacked` to read the bodies; acknowledge with `ack <id>` after reading.\n{lines}"
    with open(launched_path,"a") as lf:
        for m in recs: lf.write(f"{addr} {m['id']}\n")
    with open(os.path.join(st,"poll.log"),"a") as log:
        log.write(f"{time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())} launch {addr} ids:{' '.join(m['id'] for m in recs)}\n")
    cmd=[a.replace("{{prompt}}", prompt) for a in c["cmd"]]
    tag=time.strftime("%H%M%S")+"-"+addr.replace(":","_")
    out=open(os.path.join(st, f"session-{tag}.log"),"w")
    subprocess.Popen(cmd, cwd=c["cwd"], stdin=subprocess.DEVNULL, stdout=out, stderr=subprocess.STDOUT, start_new_session=True)
EOF
  sleep "$POLL"
done
echo "$(date -u +%FT%TZ) poller ended" >> "$LOG"

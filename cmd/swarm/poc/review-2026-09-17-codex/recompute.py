import json, pathlib, datetime, collections, statistics
root=pathlib.Path('/Users/mh/dev/workbench/.claude/worktrees/codex/swarm-adversarial-review/cmd/swarm/poc/runs')
def rows(p): return [json.loads(x) for x in p.read_text().splitlines() if x.strip()] if p.exists() else []
def ts(x):return datetime.datetime.fromisoformat(x.replace('Z','+00:00')).timestamp()
out={}
for mode in ['flat','tree','scale-flat','scale-tree']:
 p=root/mode;s=rows(p/'sessions.jsonl');w=rows(p/'state/wakes.jsonl');e=rows(p/'state/events.jsonl');d=rows(p/'state/decisions.jsonl')
 asks={x['request']:x for x in e if x['kind']=='ask'}; wait={}
 for k in ['claim','rule']:
  first={}
  for x in e:
   if x['kind']==k and x.get('request') in asks:first.setdefault(x['request'],ts(x['at'])-ts(asks[x['request']]['at']))
  wait[k]={'n':len(first),'median_s':statistics.median(first.values()) if first else None,'max_s':max(first.values(),default=None),'missing':sorted(set(asks)-set(first))}
 alltimes=[ts(x[k]) for x in s+w for k in ['started','ended']]
 out[mode]={'sessions':dict(collections.Counter(x['kind'] for x in s)),'wake_sessions':len(w),'output_tokens_sessions':sum(x.get('output_tokens',0) for x in s),'output_tokens_wakes':sum(x.get('output_tokens',0) for x in w),'cost_recorded':sum(x.get('cost_usd',0) for x in s+w),'session_wall_s':max(ts(x['ended']) for x in s)-min(ts(x['started']) for x in s),'including_wakes_wall_s':max(alltimes)-min(alltimes),'event_span_s':max(ts(x['at']) for x in e)-min(ts(x['at']) for x in e),'asks_by_tier':dict(collections.Counter(x.get('tier') for x in asks.values())),'rulers_by_tier':dict(collections.Counter(x.get('tier') for x in e if x['kind']=='rule')),'wait':wait,'killed':[{k:x.get(k) for k in ['seat','duration_s','cost_usd','output_tokens']} for x in s if x.get('killed')],'event_counts':dict(collections.Counter(x['kind'] for x in e)),'duplicate_request_decisions':{k:v for k,v in collections.Counter(x.get('request') for x in d if x.get('request')).items() if v>1},'lead_output_tokens':sum(x.get('output_tokens',0) for x in s if x['kind']=='lead')}
print(json.dumps(out,indent=2))

# Exhaust all 6 program-order-preserving interleavings of two intend/check pairs.
import itertools,json
ops=['Ai','Ac','Bi','Bc']
traces=[p for p in itertools.permutations(ops) if p.index('Ai')<p.index('Ac') and p.index('Bi')<p.index('Bc')]
result={}
for shared in [True,False]:
 bad=[]
 for tr in traces:
  published=set();clear={}
  for op in tr:
   who,kind=op
   if kind=='i':published.add(who)
   else:clear[who]=not bool((published-{who}) if shared else set())
  if all(clear.values()):bad.append(tr)
 result['shared_linearizable' if shared else 'separate_unreplicated_views']={'interleavings':len(traces),'both_clear':len(bad),'example':bad[:1]}
# A clear check is not a persistent grant: demonstrate policy change after check.
result['limits']='Only proves two compliant peers cannot both observe clear with durable monotone intents and shared fresh reads; no write fencing, no rule-order enforcement, no intent withdrawal or crash identity.'
print(json.dumps(result,indent=2))

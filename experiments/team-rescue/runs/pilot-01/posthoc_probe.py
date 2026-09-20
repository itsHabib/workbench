import hashlib, http.client, json, sys, tempfile
from pathlib import Path
HERE=Path(__file__).resolve().parent
sys.path.insert(0,str(HERE.parent.parent))
from verifier import Candidate, Recipient
root=HERE
subjects=[('pair-first-green',root/'candidates/pair-first-green'),('reference-solo',root/'candidates/reference-solo'),('pair-latest',root/'candidates/pair')]

def request(c,method,path,body):
    data=json.dumps(body).encode()
    connection=http.client.HTTPConnection('127.0.0.1',c.port,timeout=3)
    try:
        connection.request(method,path,data,{'Content-Type':'application/json'})
        response=connection.getresponse(); raw=response.read(); ctype=response.getheader('Content-Type')
        try: value=json.loads(raw)
        except Exception: value=raw.decode(errors='replace')[:180]
        return {'status':response.status,'content_type':ctype,'body':value}
    except Exception as exc:
        return {'error':f'{type(exc).__name__}: {exc}'}
    finally: connection.close()

results=[]
with tempfile.TemporaryDirectory(prefix='team-rescue-posthoc-') as temporary:
    temporary=Path(temporary)
    for label,version in subjects:
        original=(version/'service.py').read_bytes(); workspace=temporary/label; workspace.mkdir(); (workspace/'service.py').write_bytes(original)
        row={'label':label,'source':str((version/'service.py').relative_to(HERE)),'sha256':hashlib.sha256(original).hexdigest(),'evidence_kind':'POST-HOC NOT SCORED'}
        c=Candidate(workspace,workspace/'types.sqlite').start()
        try:
            row['probe1_invalid_types']={'subscription_url_17':request(c,'POST','/subscriptions',{'url':17}),'event_id_17':request(c,'POST','/events',{'id':17,'payload':{}}),'delivery_rows':c.ok('GET','/deliveries')}
        finally: c.close()
        c=Candidate(workspace,workspace/'method.sqlite').start()
        try: row['probe2_unsupported_method']=request(c,'PUT','/not-an-endpoint',{})
        finally: c.close()
        database=workspace/'scalar.sqlite'
        with Recipient() as recipient:
            c=Candidate(workspace,database).start()
            try:
                sub=c.ok('POST','/subscriptions',{'url':recipient.url})
                event=request(c,'POST','/events',{'id':'scalar-payload','payload':17})
                before=c.ok('GET','/metrics')
            finally: c.close()
            c=Candidate(workspace,database).start()
            try:
                after=c.ok('GET','/metrics'); tick=request(c,'POST','/tick',{'now':100})
                row['probe3_scalar_restart']={'event':event,'before':before,'after':after,'tick':tick,'recipient_receipts':recipient.receipts}
            finally: c.close()
        row['live_source_unchanged']=(version/'service.py').read_bytes()==original
        results.append(row)
    print(json.dumps(results,indent=2))

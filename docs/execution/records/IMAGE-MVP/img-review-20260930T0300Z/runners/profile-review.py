import datetime, hashlib, json, ssl, sys
from pathlib import Path
root=Path('/home/chabking/ani-image-mvp-runs/img-review-20260930T0300Z')
paths=[Path('/home/chabking/ani-image-mvp-runs/img-20260929T1718Z/inputs/live-profile.json'),Path('/home/chabking/ani-image-mvp-runs/pod-pull-20260930T0200Z/profile.json')]
profiles=[]
for p in paths:
    raw=p.read_bytes();v=json.loads(raw)
    profiles.append(dict(path=str(p),sha256=hashlib.sha256(raw).hexdigest(),format=v.get('format'),approved=v.get('approved'),scope=v.get('scope'),product_governance_url_present=bool(v.get('smoke',{}).get('governance_url')),resource_sha=v.get('smoke',{}).get('resource_sha'),governance_sha=v.get('smoke',{}).get('governance_sha')))
source=root/'repo/resource/fa56b55dbbc892bc40586a8303bff54826764e7e/scripts/image_smoke.py'
text=source.read_text()
result=dict(time=datetime.datetime.now(datetime.timezone.utc).isoformat(),host='fedora',profiles=profiles,decision='blocked',reason='No approved live profile covers this repair candidate and product Governance-to-Resource calls; technical Pod authorization does not permit product deployment.',shared_api_calls=0,shared_writes=0,python=sys.version.split()[0],ssl=ssl.OPENSSL_VERSION,strict_default=bool(ssl.create_default_context().verify_flags&ssl.VERIFY_X509_STRICT),smoke_source_sha='fa56b55dbbc892bc40586a8303bff54826764e7e',smoke_sha256=hashlib.sha256(source.read_bytes()).hexdigest(),strict_context_unchanged="ssl.create_default_context(cafile=ca)" in text,anonymous_systeminfo_unchanged="self.http(self.h['management_url'], self.h['ca_file'], 'GET', '/api/v2.0/systeminfo')" in text,certificate_current_state='not_verified: no fresh live TLS probe; prior CA Key Usage failure remains historical evidence',next_step='Obtain a product-backend live profile with endpoints, exact SHAs, test tenants and scoped credentials; resolve strict CA compatibility under explicit authorization and use authorized authenticated version discovery. Do not disable TLS validation or substitute the technical Pod driver.')
(root/'evidence/live-profile-review.json').write_text(json.dumps(result,indent=2,ensure_ascii=False)+'\n')
print(json.dumps(result,indent=2,ensure_ascii=False))

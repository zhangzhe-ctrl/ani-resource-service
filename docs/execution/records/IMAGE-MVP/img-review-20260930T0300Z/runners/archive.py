import datetime, hashlib, json, re, shutil, subprocess, tarfile
from pathlib import Path
root=Path('/home/chabking/ani-image-mvp-runs/img-review-20260930T0300Z')
rsha='fa56b55dbbc892bc40586a8303bff54826764e7e'
gsha='e608cba9bf7c5ccfdb3dad41471525ff541c1ad1'
assert (root/'evidence/regression-supervisor.exit').read_text().strip()=='0'
out=root/'public-return-v1';out.mkdir()
results=[];ids=set()
for ev in sorted((root/'evidence').iterdir()):
    if not ev.is_dir():continue
    log=ev/'output.txt';exitfile=ev/'exit'
    assert log.exists() and exitfile.exists(),('incomplete evidence',ev.name)
    text=log.read_text();rc=int(exitfile.read_text().strip())
    expected_red=ev.name.startswith(('r1-red-eb','r1-uncertain-red-ad','r2-red-46','r3-red-valid-71','gov-r3-red-610'))
    fixture_failure=ev.name.startswith('r3-red-ec')
    assert rc==(1 if expected_red or fixture_failure else 0),(ev.name,rc)
    results.append(dict(name=ev.name,exit=rc,classification='expected_red' if expected_red else 'fixture_setup_failure' if fixture_failure else 'pass',log_sha256=hashlib.sha256(log.read_bytes()).hexdigest()))
    dest=out/'evidence'/ev.name;dest.mkdir(parents=True)
    for name in ['output.txt','exit','created-containers.txt']:
        if (ev/name).exists():shutil.copyfile(ev/name,dest/name)
    ids.update(re.findall(r'(?<![a-z])(?:owned_fixture|fixture|container|PostgreSQL|pg|redis)=([0-9a-f]{64})(?![0-9a-f])',text))
cleanup=[]
for ident in sorted(ids):
    p=subprocess.run(['docker','inspect',ident],capture_output=True,text=True)
    assert p.returncode==1 and 'no such object' in p.stderr.lower(),('fixture still exists or lookup failed',ident,p.returncode)
    cleanup.append(dict(id=ident,inspect_exit=p.returncode,absent=True))
for repo,sha in [('resource',rsha),('governance',gsha)]:
    src=root/'repo'/repo/sha
    assert subprocess.check_output(['git','-C',str(src),'rev-parse','HEAD'],text=True).strip()==sha
    assert not subprocess.check_output(['git','-C',str(src),'status','--porcelain'],text=True)
    excluded=['docs/execution/status.md','docs/execution/records/IMAGE-MVP/img-review-20260930T0300Z/'] if repo=='resource' else []
    entries=[]
    for entry in subprocess.check_output(['git','-C',str(src),'ls-tree','-rz','--full-tree',sha]).decode().split('\0'):
        if not entry:continue
        meta,path=entry.split('\t',1)
        if any(path.startswith(x) if x.endswith('/') else path==x for x in excluded):continue
        mode,kind,blob=meta.split()
        entries.append(dict(path=path,mode=mode,kind=kind,git_blob=blob))
    manifest=dict(repository=repo,tested_sha=sha,selection='all tracked paths except the exact current ledger and this repair evidence directory; no runtime, tests, specs or generation inputs excluded',excluded_paths=excluded,entries=entries)
    (out/(repo+'-runtime-source.json')).write_text(json.dumps(manifest,indent=2)+'\n')
    # Generated API is byte-identical to the pinned contract consumed by Governance.
    if repo=='resource':
        subprocess.run(['git','-C',str(src),'diff','--exit-code','71aa986078dfb64388d783a7b2de01b7a94b0027',sha,'--','api/image'],check=True)
        subprocess.run(['git','-C',str(src),'diff','--exit-code','8f317deef04d034e7b7e0459aacb25a70f01360c',sha,'--','api/network','migrations','internal/data/network','AGENTS.md','README.md','scripts/integration'],check=True)
(out/'test-results.json').write_text(json.dumps(results,indent=2)+'\n')
(out/'cleanup.json').write_text(json.dumps(dict(time=datetime.datetime.now(datetime.timezone.utc).isoformat(),containers=cleanup,shared_api_calls=0,shared_resources_created=[],note='Database roles/databases and private DSN directories also cleaned by successful fixture traps and Resource helper exit=0; no shared cleanup attempted.'),indent=2)+'\n')
for name in ['regression-supervisor.txt','regression-supervisor.exit','live-profile-review.json']:
    shutil.copyfile(root/'evidence'/name,out/name)
for f in sorted((root/'generated-return').glob('*/manifest.json')):
    manifest=json.loads(f.read_text());assert manifest['files']==[]
    dest=out/'generated-return'/f.parent.name;dest.mkdir(parents=True);shutil.copyfile(f,dest/f.name)
for name in ['resource-run.sh','governance-run.sh','network-run.sh','regressions.sh','profile-review.py','doc-check.py','archive.py']:
    dest=out/'runners'/name;dest.parent.mkdir(exist_ok=True);shutil.copyfile(root/'state'/name,dest)
versions=[]
for command in [['go','version'],['uname','-r'],['python3','--version'],['docker','version','--format','{{.Client.Version}}'],['/home/chabking/ani-image-mvp-runs/img-20260929T1718Z/cache/tools/buf','--version'],['/home/chabking/ani-image-mvp-runs/img-20260929T1718Z/cache/tools/sqlc','version']]:
    versions.append(dict(command=command,output=subprocess.check_output(command,text=True).strip()))
(out/'tools.json').write_text(json.dumps(versions,indent=2)+'\n')
files=sorted(p for p in out.rglob('*') if p.is_file())
(out/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+str(p.relative_to(out))+'\n' for p in files))
archive=root/'public-return-v1.tar'
with tarfile.open(archive,'w') as tar:
    for p in sorted(out.rglob('*')):
        assert not p.is_symlink()
        if p.is_file():tar.add(p,arcname=str(p.relative_to(out)),recursive=False)
print(json.dumps(dict(archive=str(archive),sha256=hashlib.sha256(archive.read_bytes()).hexdigest(),files=len(files)+1,containers_confirmed_absent=len(cleanup),results=len(results)),indent=2))

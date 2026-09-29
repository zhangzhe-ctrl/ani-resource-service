from pathlib import Path
import hashlib,json,shutil,subprocess,sys,tarfile
root=Path.home()/'ani-image-mvp-runs/img-20260929T1718Z'
code='62ecc67b839dd1040468323f8c316189a0c30653'
doc='e3142cf5ea891a7439945ef12de33ff687f8b5d6'
output=root/'img10-closeout-archive'
output.mkdir()
for batch,revision in [('IMG-10',code+'-network-regression'),('IMG-11',doc+'-docs')]:
 source=root/'evidence'/batch/revision
 assert (source/'exit').read_text().strip()=='0',(batch,'gate has not passed')
 for file in source.iterdir():
  if file.is_file():
   target=output/'evidence'/batch/revision/file.name
   target.parent.mkdir(parents=True,exist_ok=True)
   shutil.copyfile(file,target)
for name in ['resource-final-regression.sh','network-sqlc-link.sh','review-docs-run.sh','review-docs-check.py']:
 shutil.copyfile(root/'state'/name,output/name)
with (output/'supervisor.txt').open('w') as log:
 for scope in sys.argv[1:]:
  assert scope.startswith('run-p') and scope.endswith('.scope')
  log.write(scope+'\n')
  r=subprocess.run(['journalctl','--user','-u',scope,'--no-pager','-n','8'],text=True,capture_output=True,check=True)
  log.write(r.stdout+r.stderr)
ids=(root/'evidence/IMG-10'/f'{code}-network-regression/created-containers.txt').read_text().splitlines()
assert ids and len(ids)==len(set(ids))
cleanup=[]
for container in ids:
 assert len(container)==64 and all(c in '0123456789abcdef' for c in container)
 r=subprocess.run(['docker','inspect',container],text=True,capture_output=True)
 assert r.returncode==1 and 'No such object' in r.stderr,(container,'absence not confirmed')
 cleanup.append({'container_id':container,'inspect_exit':r.returncode,'status':'absent'})
for label in ['ani.image.fixture','ani.image.regression=img-20260929T1718Z-final-regression']:
 r=subprocess.run(['docker','ps','-a','--filter','label='+label,'--format','{{.ID}} {{.Names}}'],text=True,capture_output=True,check=True)
 assert not r.stdout.strip(),('unexpected remaining fixture',label,r.stdout)
(output/'cleanup.json').write_text(json.dumps({'checked_at':subprocess.check_output(['date','-u','+%FT%TZ'],text=True).strip(),'containers':cleanup,'shared_resources_created':False,'retained':['source snapshots','caches','evidence','local worktrees']},indent=2)+'\n')
files=sorted(p for p in output.rglob('*') if p.is_file())
(output/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+str(p.relative_to(output))+'\n' for p in files))
archive=root/'img10-closeout-archive.tar'
with tarfile.open(archive,'w') as tar:
 for p in sorted(output.rglob('*')):
  if p.is_file():tar.add(p,arcname=str(p.relative_to(output)),recursive=False)
print(json.dumps({'archive_sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'network_log_sha256':hashlib.sha256((output/'evidence/IMG-10'/f'{code}-network-regression/output.txt').read_bytes()).hexdigest(),'document_log_sha256':hashlib.sha256((output/'evidence/IMG-11'/f'{doc}-docs/output.txt').read_bytes()).hexdigest(),'files':len(files)+1},indent=2))

from pathlib import Path
import hashlib,json,shutil,subprocess,tarfile
root=Path.home()/'ani-image-mvp-runs/img-20260929T1718Z'
head='bd038b5a7813e6dd879ae54227955339c26e13ac'
out=root/'img11-final-archive';out.mkdir()
for attempt,rc in [('final','1'),('final-retry','0')]:
 d=root/'evidence/IMG-11'/f'{head}-{attempt}'
 assert (d/'exit').read_text().strip()==rc
 for file in d.iterdir():
  if file.is_file():
   p=out/'evidence'/attempt/file.name;p.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(file,p)
for name in ['final-docs-run.sh','final-docs-run-v2.sh','final-snapshot.py','review-docs-check.py','final-archive.py']:
 shutil.copyfile(root/'state'/name,out/name)
with (out/'supervisor.txt').open('w') as f:
 for scope in ['run-p920142-i9300000.scope','run-p920477-i9300043.scope']:
  f.write(scope+'\n');r=subprocess.run(['journalctl','--user','-u',scope,'--no-pager','-n','8'],text=True,capture_output=True,check=True);f.write(r.stdout+r.stderr)
files=sorted(p for p in out.rglob('*') if p.is_file())
(out/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+str(p.relative_to(out))+'\n' for p in files))
archive=root/'img11-final-archive.tar'
with tarfile.open(archive,'w') as t:
 for p in sorted(out.rglob('*')):
  if p.is_file():t.add(p,arcname=str(p.relative_to(out)),recursive=False)
s=json.loads((out/'evidence/final-retry/source-snapshot.json').read_text())
print(json.dumps({'archive_sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'pass_log_sha256':hashlib.sha256((out/'evidence/final-retry/output.txt').read_bytes()).hexdigest(),'files':len(files)+1,'source_git_entries_sha256':s['resource_non_document_git_entries_sha256'],'generated_files':{label:len(repo['generated_sha256']) for label,repo in s['repositories'].items()},'evidence_checks':s['archived_evidence_checksums_verified']},indent=2))

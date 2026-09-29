from pathlib import Path
import hashlib,json,subprocess,sys
root=Path.home()/'ani-image-mvp-runs/img-20260929T1718Z'
out=Path(sys.argv[1]);source=Path.cwd()
code='62ecc67b839dd1040468323f8c316189a0c30653'
govsha='d64d6ee478801795afadb4573ba25f8c2de6b7fc'
def git(repo,*args):return subprocess.check_output(['git','-C',str(repo),*args])
def source_entries(repo,sha):
 records=git(repo,'ls-tree','-rz','--full-tree',sha).split(b'\0')
 return sorted(r for r in records if r and not r.split(b'\t',1)[1].startswith(b'docs/') and r.split(b'\t',1)[1]!=b'deployments/image/README.md')
head=git(source,'rev-parse','HEAD').decode().strip()
assert source_entries(source,head)==source_entries(source,code),'post-test non-document tree changed'
result={'checked_at':subprocess.check_output(['date','-u','+%FT%TZ'],text=True).strip(),'execution_host':subprocess.check_output(['hostname'],text=True).strip(),'tested_resource_code':code,'resource_non_document_git_entries_sha256':hashlib.sha256(b'\0'.join(source_entries(source,head))).hexdigest(),'repositories':{}}
for label,repo,expected,base in [('resource',source,head,'a4ca2a0fcb18346fff27f682a5244874ca4c60c8'),('governance',root/'repo/governance'/govsha,govsha,'d1a804f1d35d7294cb7eab48ee3f256d9d2482b5')]:
 assert git(repo,'rev-parse','HEAD').decode().strip()==expected
 assert not git(repo,'status','--porcelain').strip()
 generated={}
 for name in git(repo,'ls-files','-z').decode().split('\0'):
  if not name or not (name.endswith('.go') or name.endswith('.yaml') or name.endswith('.json')):continue
  p=repo/name
  if not p.is_file() or p.is_symlink():continue
  data=p.read_bytes()
  if b'Code generated' in data[:2048] or '/sqlcgen/' in name or name in ['openapi.yaml','openapi.json']:
   generated[name]=hashlib.sha256(data).hexdigest()
 result['repositories'][label]={'head':expected,'tree':git(repo,'rev-parse',expected+'^{tree}').decode().strip(),'status':'clean','baseline':base,'changed_paths':git(repo,'diff','--name-status',base,expected).decode().splitlines(),'generated_sha256':generated}
base=source/'docs/execution/records/IMAGE-MVP/img-20260929T1718Z'
checks={}
for folder in ['governance','platform','smoke','regression']:
 folder_path=base/folder
 count=0
 for line in (folder_path/'SHA256SUMS').read_text().splitlines():
  digest,name=line.split('  ',1)
  p=(folder_path/name).resolve();assert p.is_relative_to(folder_path.resolve())
  assert hashlib.sha256(p.read_bytes()).hexdigest()==digest,(folder,name)
  count+=1
 checks[folder]=count
result['archived_evidence_checksums_verified']=checks
out.write_text(json.dumps(result,indent=2,ensure_ascii=False)+'\n')
print('final snapshot: PASS; source equality, repository SHAs, generated hashes and archived evidence verified')
print('snapshot_sha256='+hashlib.sha256(out.read_bytes()).hexdigest())

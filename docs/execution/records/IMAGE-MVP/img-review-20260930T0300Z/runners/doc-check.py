from pathlib import Path
import collections,hashlib,json,re,subprocess,sys,urllib.parse
root=Path.cwd();repo=sys.argv[1];tested=sys.argv[2];base=sys.argv[3];manifest=Path(sys.argv[4])
head=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
changes=subprocess.check_output(['git','diff','--name-only',tested,head],text=True).splitlines()
excluded=['docs/execution/status.md','docs/execution/records/IMAGE-MVP/img-review-20260930T0300Z/'] if repo=='resource' else []
def is_excluded(path):
    return any(path.startswith(x) if x.endswith('/') else path==x for x in excluded)
assert all(is_excluded(p) for p in changes),changes
entries=[]
for entry in subprocess.check_output(['git','ls-tree','-rz','--full-tree',head]).decode().split('\0'):
    if not entry:continue
    meta,path=entry.split('\t',1)
    if is_excluded(path):continue
    mode,kind,blob=meta.split();entries.append(dict(path=path,mode=mode,kind=kind,git_blob=blob))
expected=json.loads(manifest.read_text());assert expected['repository']==repo and expected['tested_sha']==tested
assert expected['excluded_paths']==excluded
assert entries==expected['entries'],'runtime/build/test inputs differ from tested source'
assert not subprocess.check_output(['git','status','--porcelain'],text=True)
def anchors(path):
    result=set();counts=collections.Counter();fence=False
    for line in path.read_text().splitlines():
        if line.startswith('```'):fence=not fence
        if fence:continue
        m=re.match(r'^#{1,6}\s+(.+?)\s*#*$',line)
        if not m:continue
        title=m[1].strip().lower().replace('`','')
        title=''.join(c for c in title if c in '-_ ' or c.isalnum());slug=title.replace(' ','-');n=counts[slug];counts[slug]+=1
        result.add(slug if n==0 else f'{slug}-{n}')
    return result
files=subprocess.check_output(['git','diff','--name-only',base,head],text=True).splitlines()
checked=0;failures=[]
for name in files:
    p=root/name
    if p.suffix!='.md' or not p.is_file():continue
    fence=False
    for line in p.read_text().splitlines():
        if line.startswith('```'):fence=not fence
        if fence:continue
        for target in re.findall(r'\[[^\]]*\]\(([^)]+)\)',line):
            if target.startswith(('http:','https:','mailto:','codex:')):continue
            target=target.strip('<>');parts=target.split('#',1);raw=urllib.parse.unquote(parts[0]);dest=(p.parent/raw).resolve() if raw else p
            checked+=1
            if not dest.exists():failures.append(f'{name}: missing {target}');continue
            if len(parts)==2 and parts[1] and dest.suffix=='.md' and urllib.parse.unquote(parts[1]) not in anchors(dest):failures.append(f'{name}: missing anchor {target}')
assert not failures,'\n'.join(failures)
print(json.dumps(dict(result='pass',repository=repo,source_sha=head,tested_sha=tested,tree_sha=subprocess.check_output(['git','rev-parse',head+'^{tree}'],text=True).strip(),runtime_manifest_sha256=hashlib.sha256(manifest.read_bytes()).hexdigest(),runtime_entries=len(entries),checked_document_links=checked,documentation_only_after_test=changes),indent=2))

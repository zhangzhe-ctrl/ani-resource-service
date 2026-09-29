from pathlib import Path
import collections,hashlib,json,re,subprocess,sys,unicodedata,urllib.parse
root=Path.cwd();code='62ecc67b839dd1040468323f8c316189a0c30653';head=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
changes=subprocess.check_output(['git','diff','--name-only',code,head],text=True).splitlines()
assert all(p.startswith('docs/') or p=='deployments/image/README.md' for p in changes),changes
subprocess.run(['git','diff','71aa986078dfb64388d783a7b2de01b7a94b0027',code,'--exit-code','--','api/image'],check=True)
subprocess.run(['git','diff','a4ca2a0fcb18346fff27f682a5244874ca4c60c8',head,'--exit-code','--',':(glob)migrations/*.sql','api/network','README.md','AGENTS.md','scripts/integration','internal/data/network/sqlcgen'],check=True)
files=subprocess.check_output(['git','diff','--name-only','a4ca2a0',head],text=True).splitlines()

def anchors(path):
 result=set();counts=collections.Counter();fence=False
 for line in path.read_text().splitlines():
  if line.startswith('```'):fence=not fence
  if fence:continue
  m=re.match(r'^#{1,6}\s+(.+?)\s*#*$',line)
  if not m:continue
  title=m[1].strip().lower().replace('`','')
  title=''.join(c for c in title if c in '-_ ' or c.isalnum())
  slug=title.replace(' ','-');n=counts[slug];counts[slug]+=1
  result.add(slug if n==0 else f'{slug}-{n}')
 return result

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
   target=target.strip('<>');parts=target.split('#',1);raw=urllib.parse.unquote(parts[0])
   dest=(p.parent/raw).resolve() if raw else p
   checked+=1
   if not dest.exists():failures.append(f'{name}: missing {target}');continue
   if len(parts)==2 and parts[1] and dest.suffix=='.md' and urllib.parse.unquote(parts[1]) not in anchors(dest):
    failures.append(f'{name}: missing anchor {target}')
assert not failures,'\n'.join(failures)
print(json.dumps({'result':'pass','source_sha':head,'tested_code_sha':code,'source_tree_sha':subprocess.check_output(['git','rev-parse',head+'^{tree}'],text=True).strip(),'checked_document_links':checked,'post_test_changes':changes,'runtime_contract_matches_governance_dependency':True},indent=2))

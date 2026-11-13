import hashlib
import json
from pathlib import Path
import platform
import tarfile
import time

root=Path('/srv/juicefs-open-dir-20261113')
assert (root/'batch.exit').read_text().strip()=='0'
checks={}
files=set()
for name,expected in [('small-01',54),('large16-01',18),('large64-01',18)]:
    directory=root/name
    run=json.loads((directory/'run.json').read_text())
    assert run['passed'] and run['mounts_closed'] and run['redis_stopped']
    assert len(run['cells'])==expected
    assert all(c['valid'] and c['observed']['object_gets']==c['observed']['object_get_bytes']==0 for c in run['cells'])
    if name.startswith('large'):
        assert len(run['files']['many10000'])==10000
        assert all(c['read_ops']==20000 and c['read_bytes']==81920000 for c in run['cells'])
    checks[name]=len(run['cells'])
    files.update(p for p in directory.glob('*') if p.is_file())
    files.update(p for p in (directory/'cells').rglob('*') if p.is_file())
    files.update((directory/'redis').glob('*.log'))
fuse=json.loads((root/'fuse-validation/result.json').read_text())
assert fuse['passed'] and fuse['redis_stopped']
assert str(root) not in Path('/proc/mounts').read_text()
for proc in Path('/proc').iterdir():
    if not proc.name.isdigit(): continue
    try: command=(proc/'cmdline').read_bytes().split(b'\0')[0].decode(errors='replace')
    except (FileNotFoundError,PermissionError,ProcessLookupError): continue
    assert not command.startswith(str(root/'bin')+'/'),proc.name
files.update(p for p in (root/'scripts').glob('*') if p.is_file())
files.update(p for p in (root/'fuse-validation').glob('*') if p.is_file())
files.update(p for p in (root/'fuse-validation/smoke').glob('*') if p.is_file())
files.update(root/name for name in ['batch.log','batch.exit','postflight.log','small-report.log','large16-report.log','large64-report.log'])
collection={'host':platform.uname()._asdict(),'unix_seconds':time.time(),'checks':checks,
            'fuse_passed':True,'task_mounts_remaining':[],'task_binary_processes_remaining':[],
            'excluded':'binaries, generated objects and Redis persistence; file input reproducible from fixed seeds'}
(root/'collection.json').write_text(json.dumps(collection,indent=2)+'\n')
files.add(root/'collection.json')
manifest=''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+str(p.relative_to(root))+'\n' for p in sorted(files))
(root/'RAW-SHA256SUMS.txt').write_text(manifest)
files.add(root/'RAW-SHA256SUMS.txt')
archive=root/'a355-open-dir-10000.tar.gz'
assert not archive.exists()
with tarfile.open(archive,'w:gz',compresslevel=6) as stream:
    for path in sorted(files): stream.add(path,arcname=str(path.relative_to(root)),recursive=False)
result={'sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'bytes':archive.stat().st_size,'files':len(files),'checks':checks}
(root/'archive.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result))

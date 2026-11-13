import json
from pathlib import Path
import sys
import tarfile
import tempfile

repo = Path('/mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source')
sys.path.insert(0, str(repo / 'hack'))
from lazy_clone_txn_report import report, counters

out = Path(tempfile.mkdtemp(prefix='jfs-txn-report-'))
archive = repo / 'hack/lazy_clone_results/20261113-a355-optimized/a355-forkredis-optimized.tar.gz'
with tarfile.open(archive) as stream:
    stream.extractall(out, members=[m for m in stream.getmembers() if m.name.startswith('quick-01/')], filter='data')
result = report(out / 'quick-01')
assert result['cells'] == 6
assert not any(g['instrumented'] for g in result['groups'])
metric = out / 'test.prom'
metric.write_text('juicefs_forkredis_phase_seconds_total{mountpoint="/test",phase="begin"} 0.125\n'
                  'juicefs_forkredis_cache_hits_total{kind="record"} 2\n'
                  'juicefs_forkredis_cache_hits_total{kind="range"} 3\n')
values = counters(metric)
assert values[('forkredis_phase_seconds_total', 'begin')] == 0.125
assert values[('forkredis_cache_hits_total', '')] == 5
print(json.dumps({'historical_copy_cells': result['cells'], 'phase_and_kind_parser': 'passed',
                  'history_modified': False, 'copy_output': str(out)}))

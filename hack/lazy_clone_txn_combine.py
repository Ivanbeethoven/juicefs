#!/usr/bin/env python3
"""Combine all paired cells from compatible transaction timing runs."""
import argparse
import csv
import hashlib
import json
from pathlib import Path
import statistics


def combine(directories, out):
    rows, sources = [], []
    reference = None
    for index, directory in enumerate(directories):
        raw = (directory / 'run.json').read_bytes()
        run = json.loads(raw)
        assert run['passed'] and run['mounts_closed'] and run['redis_stopped']
        assert all(c['valid'] for c in run['cells'])
        signature = {k: run[k] for k in ('binaries', 'script_sha256', 'files', 'uname')}
        signature['parameters'] = {k: v for k, v in run['parameters'].items() if k not in ('out', 'profile', 'pairs')}
        if reference is None:
            reference = signature
        assert signature == reference, 'build, input, host or parameter mismatch'
        sources.append({'directory': str(directory), 'run_sha256': hashlib.sha256(raw).hexdigest(),
                        'cells': len(run['cells'])})
        with (directory / 'cell-summary.csv').open() as stream:
            operations = {r['label']: r for r in csv.DictReader(stream)}
        with (directory / 'txn-cells.csv').open() as stream:
            for values in csv.DictReader(stream):
                row = dict(values)
                row.update({k: v for k, v in operations[row['label']].items() if k not in row})
                for key in row:
                    if key not in ('label', 'variant', 'profile', 'fixture', 'arm', 'instrumented'):
                        row[key] = float(row[key])
                row['run_index'] = index
                rows.append(row)
    groups = []
    for profile, fixture in sorted({(r['profile'], r['fixture']) for r in rows}):
        selected = [r for r in rows if (r['profile'], r['fixture']) == (profile, fixture)]
        pairs = {}
        for r in selected:
            key = (r['run_index'], r['pair'])
            assert (r['variant'], r['arm']) not in pairs.setdefault(key, {})
            pairs[key][r['variant'], r['arm']] = r
        expected = {(v, a) for v in ('before', 'after') for a in ('lower', 'overlay', 'native')}
        assert all(set(p) == expected for p in pairs.values())
        medians = {}
        for variant, arm in sorted(expected):
            subset = [r for r in selected if (r['variant'], r['arm']) == (variant, arm)]
            medians[variant + '_' + arm] = {k: statistics.median(r[k] for r in subset) for k in subset[0]
                                          if isinstance(subset[0][k], (float, int)) and k not in ('pair', 'run_index')}
        ratios = {v: [p[v, 'overlay']['wall_ms']/p[v, 'native']['wall_ms'] for p in pairs.values()]
                  for v in ('before', 'after')}
        reductions = [(1-p['after', 'overlay']['wall_ms']/p['before', 'overlay']['wall_ms'])*100 for p in pairs.values()]
        layer = [(p['after', 'overlay']['wall_ms']/p['after', 'lower']['wall_ms']-1)*100 for p in pairs.values()]
        groups.append({'profile': profile, 'fixture': fixture, 'pairs': len(pairs), 'medians': medians,
                       'overlay_vs_native_ratios': ratios, 'median_ratios': {v: statistics.median(a) for v, a in ratios.items()},
                       'time_reduction_percent': reductions, 'median_time_reduction_percent': statistics.median(reductions),
                       'layer_overhead_percent': layer, 'median_layer_overhead_percent': statistics.median(layer)})
    out.mkdir(parents=True, exist_ok=False)
    with (out / 'cells.csv').open('w', newline='') as stream:
        writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    result = {'sources': sources, 'cells': len(rows), 'compatible_inputs': reference, 'groups': groups}
    (out / 'summary.json').write_text(json.dumps(result, indent=2)+'\n')
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directories', nargs='+', type=Path)
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    result = combine(args.directories, args.out)
    print(json.dumps({'cells': result['cells'], 'groups': [
        {k: g[k] for k in ('profile', 'fixture', 'pairs', 'median_ratios', 'median_time_reduction_percent',
                           'median_layer_overhead_percent')} for g in result['groups']]}, indent=2))

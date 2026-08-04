#!/usr/bin/env python3
from pathlib import Path
import sys
import yaml

ROOT = Path(__file__).resolve().parents[1]
CANON = ROOT / 'docs' / 'canonical'
required = [
    'organization.yaml','role-catalog.yaml','leader-worker-map.yaml','model-routing.yaml',
    'capability-matrix.yaml','instruction-precedence.yaml','memory-policy.yaml',
    'reasoning-assurance.yaml','cell-boundaries.yaml','architecture-characteristics.yaml',
    'source-manifest.yaml','decisions-required.yaml'
]

errors = []
data = {}
for name in required:
    path = CANON / name
    if not path.exists():
        errors.append(f'missing: {name}')
        continue
    try:
        data[name] = yaml.safe_load(path.read_text(encoding='utf-8'))
    except Exception as exc:
        errors.append(f'invalid yaml {name}: {exc}')

if not errors:
    org = data['organization.yaml']
    roles = data['role-catalog.yaml']['roles']
    leader_map = data['leader-worker-map.yaml']['departments']
    ids = [r['id'] for r in roles]
    if len(ids) != len(set(ids)):
        errors.append('duplicate role ids')
    if len(org['operational_departments']) != 7:
        errors.append('operational department count must be 7')
    role_ids = set(ids)
    for d in leader_map:
        if d['leader'] not in role_ids:
            errors.append(f"missing leader role: {d['leader']}")
        for worker in d['workers']:
            if worker not in role_ids:
                errors.append(f'missing worker role: {worker}')
    research = next(x for x in org['transversal_units'] if x['id'] == 'investigacion')
    if not research['leaderless']:
        errors.append('research must be leaderless')
    if data['capability-matrix.yaml']['default_policy'] != 'deny':
        errors.append('capability policy must default deny')
    cell = data['cell-boundaries.yaml']['cells'][0]
    if cell['organization_direct_database_access']:
        errors.append('organization must not have direct cell DB access')

if errors:
    print('BRANCH 0 VALIDATION FAILED')
    for error in errors:
        print(f'- {error}')
    sys.exit(1)

print('BRANCH 0 VALIDATION OK')
print(f"roles: {len(data['role-catalog.yaml']['roles'])}")
print(f"departments: {len(data['organization.yaml']['operational_departments'])}")
print(f"open decisions: {len(data['decisions-required.yaml']['open'])}")

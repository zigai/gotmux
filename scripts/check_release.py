#!/usr/bin/env python3
"""Fail until every separately evidenced v1 release/consumer gate is completed."""
from pathlib import Path
import json
import sys
root=Path(__file__).resolve().parents[1]
data=json.loads((root/'internal/schema/release.json').read_text())
acceptance=json.loads((root/'internal/schema/acceptance.json').read_text())
ledger=json.loads((root/'internal/schema/commands.json').read_text())
missing=[name for name,status in data['gates'].items() if status!='passed']
missing += [row['id'] for row in acceptance['gates'] if row['status']!='passed' or not row['consumer_test'] or not row['evidence']]
if not ledger['exhaustive_flag_parity']:missing.append('exhaustive command/flag review')
if not data['release_ready'] or missing:
    print('NOT V1-READY. Outstanding gates:')
    print('\n'.join('  - '+item for item in missing))
    sys.exit(1)
print('Release prerequisites are recorded as passed; verify their retained evidence before tagging.')

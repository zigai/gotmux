#!/usr/bin/env python3
"""Validate the alpha inventory's structure, not full upstream release parity."""
from pathlib import Path
import json
import sys
root=Path(__file__).resolve().parents[1]
data=json.loads((root/'internal/schema/commands.json').read_text())
seen=set()
for row in data['entries']:
    command=row['command']
    if command in seen: sys.exit(f'duplicate command: {command}')
    seen.add(command)
    for field in ('symbol','minimum_version','transport','completion','test','status'):
        if not row.get(field): sys.exit(f'{command}: missing {field}')
    if row['status']=='raw-only-alpha' and not row.get('reason'):
        sys.exit(f'{command}: raw-only support needs a reason')
required={'display-message','list-sessions','list-windows','list-panes','list-clients','capture-pane','new-session','new-window','split-window','send-keys','show-options','set-option','load-buffer','save-buffer','show-buffer','attach-session','refresh-client','wait-for','set-hook','bind-key'}
if missing:=required-seen:sys.exit('missing initial command entries: '+', '.join(sorted(missing)))
print(f'Alpha ledger structure valid: {len(seen)} commands. Full flag parity is NOT certified.')

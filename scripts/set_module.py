#!/usr/bin/env python3
"""Replace the provisional module path before publishing (does not run Go)."""
from pathlib import Path
import argparse
import re
root=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(description=__doc__);p.add_argument('module');a=p.parse_args()
if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._~/-]+',a.module) or '/' not in a.module or '..' in a.module or '//' in a.module:p.error('expected an ordinary Go repository module path, not a URL')
old=re.search(r'^module\s+(\S+)',(root/'go.mod').read_text(),re.M).group(1)
for path in root.rglob('*'):
    if '.git' in path.parts or not path.is_file():continue
    if path.name!='go.mod' and path.suffix not in {'.go','.md','.json','.yml','.yaml','.py','.sh'}:continue
    text=path.read_text()
    if old in text:path.write_text(text.replace(old,a.module))
print(f'Module changed from {old} to {a.module}; run go mod tidy and the test suite.')

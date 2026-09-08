#!/usr/bin/env python3
"""Read list-commands from an explicitly provided private socket; never starts a daemon."""
import argparse
import json
from pathlib import Path
import subprocess
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--binary',default='tmux');p.add_argument('--socket',required=True);p.add_argument('--output',required=True)
a=p.parse_args()
if not Path(a.socket).is_absolute():p.error('socket must be absolute and explicitly selected')
version=subprocess.run([a.binary,'-V'],check=True,capture_output=True,timeout=5).stdout.decode().strip()
# Usage contains command grammar, not user-supplied format values. Preserve the
# exact line as a version-pinned evidence artifact rather than guessing flags.
result=subprocess.run([a.binary,'-N','-S',a.socket,'list-commands'],check=True,capture_output=True,timeout=5)
rows=[]
for line in result.stdout.decode('utf-8',errors='strict').splitlines():
    name,_,usage=line.partition(' ')
    rows.append({'name':name,'raw_usage':usage})
Path(a.output).write_text(json.dumps({'executable_version':version,'commands':rows},indent=2)+'\n')
print(f'Recorded {len(rows)} command descriptions for {version}; review flags against its pinned manual.')

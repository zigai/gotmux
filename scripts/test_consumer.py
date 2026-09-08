#!/usr/bin/env python3
"""Build a clean external consumer using a temporary file proxy, with NO replace directives.
Requires the declared Go toolchain and normal network access for pinned dependencies.
Only the unpublished local module is excluded from public checksum lookup.
"""
from pathlib import Path
import json
import os
import re
import subprocess
import tempfile
import zipfile
root=Path(__file__).resolve().parents[1]
mod=(root/'go.mod').read_text();module=re.search(r'^module\s+(\S+)',mod,re.M).group(1)
go=re.search(r'^go\s+(\S+)',mod,re.M).group(1)
escaped=''.join('!'+c.lower() if c.isupper() else c for c in module)
version='v0.0.0'
with tempfile.TemporaryDirectory(prefix='tmux-consumer-') as tmp:
    base=Path(tmp);proxy=base/'proxy';prefix=proxy/escaped/'@v';prefix.mkdir(parents=True)
    (prefix/(version+'.mod')).write_text(mod)
    (prefix/(version+'.info')).write_text(json.dumps({'Version':version,'Time':'2026-09-08T00:00:00Z'}))
    (prefix/'list').write_text(version+'\n')
    with zipfile.ZipFile(prefix/(version+'.zip'),'w',zipfile.ZIP_DEFLATED) as z:
        for path in root.rglob('*'):
            if not path.is_file() or '.git' in path.parts or path.suffix=='.zip':continue
            z.write(path,f'{module}@{version}/'+path.relative_to(root).as_posix())
    consumer=base/'consumer';consumer.mkdir()
    (consumer/'go.mod').write_text(f'module consumer.test/check\n\ngo {go}\n\nrequire {module} {version}\n')
    (consumer/'main.go').write_text(f'package main\nimport ("context"; t "{module}")\nfunc main(){{var p t.Pane; _=p.Kill(context.Background()); _,_=t.NewCommand("display-message","literal")}}\n')
    env=os.environ.copy();env.update(GOWORK='off',GOPROXY=proxy.as_uri()+',https://proxy.golang.org',GONOSUMDB=module,GOMODCACHE=str(base/'cache'))
    subprocess.run(['go','mod','tidy'],cwd=consumer,env=env,check=True,timeout=180)
    subprocess.run(['go','build','./...'],cwd=consumer,env=env,check=True,timeout=180)
print('Clean external consumer build passed with GOWORK=off and no replace directives.')

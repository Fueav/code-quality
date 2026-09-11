#!/usr/bin/env python3
"""Run affected native checks; make release-check remains the full release gate."""
import hashlib, json, os, shlex, subprocess
from pathlib import Path

GROUPS = ['qualification-test','live-test','mining-test']

def python_groups(paths, cli_changed=False):
    groups = {'qualification-test'} if cli_changed else set()
    for path in paths:
        if path.startswith('pilot/live/'): groups.add('live-test')
        elif path.startswith('pilot/mining/'): groups.add('mining-test')
        elif path.startswith(('pilot/','policy/','schemas/','harness/gates/')) or path == 'Makefile': groups.update(GROUPS)
    return sorted(groups)

def go_packages(paths, packages, root):
    if paths & {'go.mod','go.sum','go.work','go.work.sum','Makefile'}: return ['./...']
    by_dir = {Path(item['Dir']).relative_to(root).as_posix(): item['ImportPath'] for item in packages}
    selected = set()
    for path in paths:
        if path.endswith('.go'):
            name = by_dir.get(Path(path).parent.as_posix())
            if name is None: return ['./...']
            selected.add(name)
    for item in packages:
        directory = Path(item['Dir']).relative_to(root)
        embedded = {str(directory / file) for key in ['EmbedFiles','TestEmbedFiles','XTestEmbedFiles'] for file in item.get(key,[])}
        if paths & embedded: selected.add(item['ImportPath'])
    if any(path.startswith(('plugins/','policy/','schemas/','.github/','docs/','harness/gates/')) or path in {'README.md','install.sh'} for path in paths):
        if '.' in by_dir: selected.add(by_dir['.'])
    while True:
        consumers = {item['ImportPath'] for item in packages if selected & set(item.get('Deps',[]) + item.get('TestImports',[]) + item.get('XTestImports',[]))}
        if consumers <= selected: return sorted(selected)
        selected.update(consumers)

def load_packages(root):
    raw = subprocess.check_output(['go','list','-json','./...'], cwd=root, text=True)
    decoder = json.JSONDecoder(); packages = []
    while raw.strip():
        item, end = decoder.raw_decode(raw.lstrip()); packages.append(item); raw = raw.lstrip()[end:]
    return packages

def shell_check(path, root):
    first = (root/path).read_text().splitlines()[0]
    words = shlex.split(first[2:]) if first.startswith('#!') else ['sh']
    interpreter = Path(words[0]).name
    if interpreter == 'env':
        words = words[1:]
        if words and words[0] == '-S': words = words[1:]
        interpreter = words[0] if words else ''
    if interpreter not in {'sh','bash','zsh'}: raise ValueError(f'unsupported shell interpreter: {path}')
    return [interpreter,'-n',path]

def main():
    root = Path(os.environ['HARNESS_PROJECT_ROOT']).resolve()
    raw = Path(os.environ['HARNESS_SNAPSHOT_FILE']).read_bytes()
    if hashlib.sha256(raw).hexdigest() != os.environ['HARNESS_SNAPSHOT_SHA256']: raise ValueError('changed-input snapshot digest mismatch')
    paths = {item['path'] for item in json.loads(raw)['changes']}
    commands = []; packages = []
    go_inputs = any(path.endswith('.go') or path.startswith(('cmd/','internal/','quality/','plugins/','policy/','schemas/','.github/','docs/','harness/gates/')) or path in {'go.mod','go.sum','go.work','go.work.sum','Makefile','README.md','install.sh'} for path in paths)
    if go_inputs:
        packages = go_packages(paths, load_packages(root), root)
        files = sorted(path for path in paths if path.endswith('.go') and (root/path).is_file())
        if files and subprocess.check_output(['gofmt','-l',*files], cwd=root, text=True).strip(): raise ValueError('changed Go files require gofmt')
        if packages:
            commands += [['go','vet',*packages], ['scripts/with_test_resources.sh','--','go','test',*packages]]
    commands += [['make',group] for group in python_groups(paths, './...' in packages or any(name.endswith('/cmd/quality-review') for name in packages))]
    commands += [shell_check(path, root) for path in sorted(paths) if path.endswith('.sh') and (root/path).is_file()]
    if any(path.startswith('harness/gates/') for path in paths): commands.append(['python3','harness/gates/native_checks_test.py'])
    for command in commands:
        print('native check: '+ ' '.join(command), flush=True)
        subprocess.run(command, cwd=root, check=True)
    print('focused native checks passed')

if __name__ == '__main__': main()

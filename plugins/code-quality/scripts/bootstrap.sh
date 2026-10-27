#!/bin/sh
# Installs a pinned CLI/plugin pair; retained rollback uses only local evidence.
set -eu
umask 077
command -v python3 >/dev/null 2>&1 || { echo 'python3 is required for verified plugin installation; nothing was changed' >&2; exit 1; }
program_dir=$(mktemp -d)
trap 'rm -rf "$program_dir"' EXIT HUP INT TERM
cat > "$program_dir/bootstrap.py" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import tempfile

MARKETPLACE = 'fueav-code-quality'
PLUGIN = 'code-quality@' + MARKETPLACE
REPOSITORY = 'https://github.com/Fueav/code-quality.git'


def run(args, **kwargs):
    result = subprocess.run([str(x) for x in args], text=True, capture_output=True, **kwargs)
    if result.returncode:
        # Host diagnostics may contain other plugins or credentials. Retain only the action.
        raise RuntimeError('command failed: ' + shlex.join([str(x) for x in args[:4]]))
    return result.stdout.strip()


def digest(path):
    value = hashlib.sha256()
    with open(path, 'rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            value.update(chunk)
    return value.hexdigest()


def tree_digest(root):
    value = hashlib.sha256()
    for path in sorted(root.rglob('*')):
        if path.is_symlink():
            raise RuntimeError('plugin backup cannot contain symlinks')
        if path.is_file():
            value.update(path.relative_to(root).as_posix().encode() + b'\0')
            value.update(bytes.fromhex(digest(path)))
        elif not path.is_dir():
            raise RuntimeError('plugin contains a non-regular file')
    return value.hexdigest()


def private_dir(path):
    if path.is_symlink():
        raise RuntimeError('installation state directory must not be a symlink')
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = path.stat()
    if info.st_uid != os.getuid() or info.st_mode & 0o022:
        raise RuntimeError('installation state directory must be owner-controlled')


def write_json(path, data):
    descriptor, raw = tempfile.mkstemp(prefix='.' + path.name + '.', dir=path.parent)
    temporary = Path(raw)
    try:
        with os.fdopen(descriptor, 'w', encoding='utf-8') as stream:
            json.dump(data, stream, indent=2, ensure_ascii=False)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        sync_directory(path.parent)
    finally:
        temporary.unlink(missing_ok=True)


def sync_directory(path):
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def sync_tree(path):
    for child in path.rglob('*'):
        if child.is_file():
            with open(child, 'rb') as stream:
                os.fsync(stream.fileno())
    for child in sorted((p for p in path.rglob('*') if p.is_dir()), reverse=True):
        sync_directory(child)
    sync_directory(path)


def host_capabilities(host):
    commands = [(['plugin', 'list'], ['--json']), (['plugin', 'marketplace', 'list'], ['--json'])]
    if host == 'codex':
        commands += [(['plugin', 'marketplace', 'add'], ['--ref']),
                     (['plugin', 'marketplace', 'remove'], []), (['plugin', 'add'], []), (['plugin', 'remove'], [])]
    else:
        commands += [(['plugin', 'marketplace', 'add'], ['--scope']),
                     (['plugin', 'install'], ['--scope']), (['plugin', 'uninstall'], ['--scope', '--keep-data'])]
    for command, required in commands:
        help_text = run([host, *command, '--help'])
        if not command_help_matches(host, command, help_text) or any(not has_option(help_text, flag) for flag in required):
            raise RuntimeError('host lacks a required plugin command capability; nothing was changed')
    scoped_remove = False
    if host == 'claude':
        help_text = run([host, 'plugin', 'marketplace', 'remove', '--help'])
        if not command_help_matches(host, ['plugin', 'marketplace', 'remove'], help_text):
            raise RuntimeError('host cannot safely remove a marketplace; nothing was changed')
        scoped_remove = has_option(help_text, '--scope')
    capabilities = {'schema_version': 1, 'claude_marketplace_remove_scope': scoped_remove}
    if host == 'claude':
        capabilities['claude_user_config'] = str(Path(os.environ.get('CLAUDE_CONFIG_DIR', str(Path.home() / '.claude'))).resolve())
    return capabilities


def has_option(help_text, option):
    return re.search(r'(?<![\w-])' + re.escape(option) + r'(?=[\s=,]|$)', help_text) is not None


def command_help_matches(host, command, help_text):
    tokens = [host, *command]
    expression = r'(?im)^usage:\s+' + r'\s+'.join(re.escape(token) + r'(?:\|[\w-]+)*' for token in tokens) + r'(?=\s|$)'
    return re.search(expression, help_text) is not None


def marketplace_declaration(path):
    try:
        info = path.lstat()
    except FileNotFoundError:
        return None
    except OSError:
        raise RuntimeError('cannot establish marketplace scope from unreadable settings') from None
    if not stat.S_ISREG(info.st_mode):
        raise RuntimeError('cannot establish marketplace scope from non-regular settings')
    try:
        settings = json.loads(path.read_text(encoding='utf-8'))
        entries = settings.get('extraKnownMarketplaces', {})
        if not isinstance(entries, dict):
            raise ValueError('invalid marketplace declarations')
        if MARKETPLACE not in entries:
            return None
        entry = entries[MARKETPLACE]
        if not isinstance(entry, dict) or not isinstance(entry.get('source'), dict):
            raise ValueError('invalid product declaration')
        return entry
    except (OSError, ValueError, AttributeError):
        raise RuntimeError('cannot establish marketplace scope from settings') from None


def validate_removal_scope(host, state, capabilities):
    if host != 'claude' or capabilities['claude_marketplace_remove_scope']:
        return
    # Older Claude lists effective marketplaces without scope. A user-scoped
    # plugin alone does not prove its marketplace is exclusively user-scoped.
    overrides = [name for name, value in os.environ.items() if value and name != 'CLAUDE_CONFIG_DIR' and
                 (re.fullmatch(r'CLAUDE_.*(?:CONFIG|SETTINGS).*', name) or name in ('CLAUDE_CODE_SIMPLE', 'CLAUDE_CODE_PLUGIN_SEED_DIR'))]
    if overrides:
        raise RuntimeError('HOLD: legacy marketplace removal cannot establish scope with configuration overrides')
    config = Path(os.environ.get('CLAUDE_CONFIG_DIR', str(Path.home() / '.claude')))
    if not config.is_absolute() or config.is_symlink():
        raise RuntimeError('HOLD: legacy marketplace removal needs a known user configuration directory')
    user_settings = config / 'settings.json'
    declaration = marketplace_declaration(user_settings)
    candidates = {config / 'settings.local.json'}
    for directory in (Path.cwd(), *Path.cwd().parents):
        candidates.update((directory / '.claude/settings.json', directory / '.claude/settings.local.json'))
    managed = Path('/Library/Application Support/ClaudeCode') if sys.platform == 'darwin' else Path('/etc/claude-code')
    candidates.add(managed / 'managed-settings.json')
    # Fragmented managed policy is not safely attributable by these older APIs.
    fragments = managed / 'managed-settings.d'
    try:
        fragments.lstat()
    except FileNotFoundError:
        pass
    except OSError:
        raise RuntimeError('HOLD: legacy marketplace removal cannot inspect managed policy scope') from None
    else:
        raise RuntimeError('HOLD: legacy marketplace removal cannot establish managed policy scope')
    for path in candidates:
        # Scope belongs to the settings path, not the inode it resolves to.
        # A project/local alias of the user file still needs fail-closed inspection.
        if os.path.abspath(path) == os.path.abspath(user_settings):
            continue
        if marketplace_declaration(path) is not None:
            raise RuntimeError('HOLD: marketplace has another or unknown scope; refusing unscoped removal')
    listed = state['marketplace']
    if listed is None:
        if declaration is not None:
            raise RuntimeError('HOLD: user marketplace declaration is absent from host state')
        return
    if not isinstance(declaration, dict) or not isinstance(declaration.get('source'), dict):
        raise RuntimeError('HOLD: legacy marketplace removal needs an explicit user marketplace declaration')
    declared = declaration['source']
    url, directory = declared.get('url'), declared.get('path')
    same_git = (declared.get('source') == listed.get('source') == 'git' and
                isinstance(url, str) and bool(url.strip()) and url == listed.get('url'))
    same_directory = (declared.get('source') == listed.get('source') == 'directory' and
                      isinstance(directory, str) and bool(directory.strip()) and directory == listed.get('installLocation'))
    if not (same_git or same_directory):
        raise RuntimeError('HOLD: user declaration does not identify the listed marketplace source')


def host_state(host, strict=False):
    marketplaces = json.loads(run([host, 'plugin', 'marketplace', 'list', '--json']))
    plugins = json.loads(run([host, 'plugin', 'list', '--json']))
    if host == 'codex':
        marketplaces = marketplaces['marketplaces']
        plugins = plugins['installed']
        installed = [x for x in plugins if x.get('pluginId') == PLUGIN]
    else:
        installed = [x for x in plugins if x.get('id') == PLUGIN]
        if any(x.get('scope') != 'user' for x in installed):
            raise RuntimeError('HOLD: Code Quality plugin has another or unknown scope')
    sources = [x for x in marketplaces if x.get('name') == MARKETPLACE]
    if len(installed) > 1 or len(sources) > 1:
        raise RuntimeError('ambiguous Code Quality installation; nothing can be replaced safely')
    plugin = installed[0] if installed else None
    source = sources[0] if sources else None
    if host == 'claude' and source and (source.get('scope', 'user') != 'user' or
                                      ('scopes' in source and source['scopes'] != ['user'])):
        raise RuntimeError('HOLD: Code Quality marketplace has another or unknown scope')
    if strict and plugin is not None and (not source or plugin.get('enabled') is not True):
        raise RuntimeError('existing Code Quality plugin is disabled or its source is unknown; preserve it for manual inspection')
    return {'plugin': plugin, 'marketplace': source}


def plugin_path(host, plugin):
    if host == 'codex':
        source = plugin.get('source', {})
        raw = source.get('path') if source.get('source') == 'local' else None
    else:
        raw = plugin.get('installPath')
    if not isinstance(raw, str) or not Path(raw).is_absolute():
        raise RuntimeError('host did not identify a local installed plugin path')
    path = Path(raw)
    if path.is_symlink() or not path.is_dir():
        raise RuntimeError('installed plugin path is not a regular directory')
    return path


def verify_plugin(host, state, version):
    plugin = state['plugin']
    if not plugin or plugin.get('version') != version or plugin.get('enabled') is not True:
        raise RuntimeError('host plugin version or enabled state did not match the requested release')
    path = plugin_path(host, plugin)
    descriptor = path / ('.codex-plugin' if host == 'codex' else '.claude-plugin') / 'plugin.json'
    payload = json.loads(descriptor.read_text(encoding='utf-8'))
    if payload.get('name') != 'code-quality' or payload.get('version') != version:
        raise RuntimeError('installed plugin descriptor does not match the host state')
    return tree_digest(path)


def marketplace_snapshot(host, state, destination):
    source = state['marketplace']
    if source is None:
        return None
    raw_root = source.get('root') if host == 'codex' else source.get('installLocation')
    if not isinstance(raw_root, str) or not Path(raw_root).is_absolute():
        raise RuntimeError('host did not identify the marketplace checkout')
    source_root = Path(raw_root)
    manifest_relative = '.agents/plugins/marketplace.json' if host == 'codex' else '.claude-plugin/marketplace.json'
    manifest = json.loads((source_root / manifest_relative).read_text(encoding='utf-8'))
    entries = manifest.get('plugins')
    if manifest.get('name') != MARKETPLACE or not isinstance(entries, list) or len(entries) != 1 or entries[0].get('name') != 'code-quality':
        raise RuntimeError('marketplace contains unrelated entries; refusing to replace it')
    old_plugin = state['plugin']
    if old_plugin:
        copy_from = plugin_path(host, old_plugin)
        verify_plugin(host, state, old_plugin['version'])
    else:
        entry_source = entries[0].get('source')
        relative = entry_source.get('path') if isinstance(entry_source, dict) else entry_source
        if relative not in ('./plugins/code-quality', 'plugins/code-quality'):
            raise RuntimeError('uninstalled marketplace source is not the Code Quality local plugin')
        copy_from = source_root / 'plugins/code-quality'
        tree_digest(copy_from)
    destination.mkdir(mode=0o700)
    shutil.copytree(copy_from, destination / 'plugins/code-quality', symlinks=True)
    tree_digest(destination / 'plugins/code-quality')
    # Preserve the already accepted marketplace schema, changing only this product's source.
    # No host settings, unrelated plugins, Git checkout, or credentials are copied.
    if host == 'codex':
        manifest['plugins'][0]['source'] = {'source': 'local', 'path': './plugins/code-quality'}
    else:
        version = json.loads((copy_from / '.claude-plugin/plugin.json').read_text())['version']
        manifest['plugins'][0].update(source='./plugins/code-quality', version=version)
    path = destination / manifest_relative
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    write_json(path, manifest)
    return tree_digest(destination)


def remove_current(host, state, capabilities):
    validate_removal_scope(host, state, capabilities)
    if state['plugin']:
        if host == 'codex':
            run([host, 'plugin', 'remove', PLUGIN])
        else:
            run([host, 'plugin', 'uninstall', PLUGIN, '--scope', 'user', '--keep-data'])
    if state['marketplace']:
        args = [host, 'plugin', 'marketplace', 'remove', MARKETPLACE]
        if host == 'claude' and capabilities['claude_marketplace_remove_scope']:
            args += ['--scope', 'user']
        run(args)


def add_plugin(host, source, version=None, install=True):
    args = [host, 'plugin', 'marketplace', 'add', source]
    if host == 'codex' and version:
        args += ['--ref', version]
    if host == 'claude':
        args += ['--scope', 'user']
    run(args)
    if install:
        if host == 'codex':
            run([host, 'plugin', 'add', PLUGIN])
        else:
            run([host, 'plugin', 'install', PLUGIN, '--scope', 'user'])


def replace_binary(source, target):
    if target.is_symlink() or (target.exists() and not target.is_file()):
        raise RuntimeError('refusing to replace a non-regular CLI')
    fd, raw = tempfile.mkstemp(prefix='.quality-review-activate.', dir=target.parent)
    os.close(fd)
    temporary = Path(raw)
    try:
        shutil.copy2(source, temporary)
        with open(temporary, 'rb') as stream:
            os.fsync(stream.fileno())
        os.replace(temporary, target)
        sync_directory(target.parent)
    finally:
        temporary.unlink(missing_ok=True)


def rollback(directory, active=False):
    state = json.loads((directory / 'transaction.json').read_text())
    host = state['host']
    capabilities = host_capabilities(host)
    if capabilities != state.get('host_capabilities'):
        raise RuntimeError('host capabilities changed; inspect retained rollback before proceeding')
    target = Path(state['binary'])
    old = directory / 'quality-review.previous'
    current = host_state(host)
    current_plugin = current['plugin']
    allowed = {state['requested_version'][1:]}
    if state['before']['plugin']:
        allowed.add(state['before']['plugin']['version'])
    if not active and current_plugin and current_plugin.get('version') not in allowed:
        raise RuntimeError('a different plugin was installed after this transaction; refusing stale rollback')
    if target.is_symlink() or (target.exists() and digest(target) not in {state.get('previous_binary_sha256'), state.get('new_binary_sha256')}):
        raise RuntimeError('a different CLI was installed after this transaction; refusing stale rollback')
    if old.exists():
        if digest(old) != state['previous_binary_sha256']:
            raise RuntimeError('retained CLI backup failed its digest check')
    snapshot = directory / 'marketplace.previous'
    if state['before']['marketplace'] and tree_digest(snapshot) != state['previous_marketplace_sha256']:
        raise RuntimeError('retained plugin backup failed its digest check')
    remove_current(host, current, capabilities)
    if state['before']['marketplace']:
        add_plugin(host, str(snapshot), install=bool(state['before']['plugin']))
    restored = host_state(host)
    if state['before']['plugin']:
        actual = verify_plugin(host, restored, state['before']['plugin']['version'])
        if actual != state['previous_plugin_sha256']:
            raise RuntimeError('restored plugin contents did not match the retained version')
    elif restored['plugin']:
        raise RuntimeError('fresh installation rollback left a plugin installed')
    if old.exists():
        replace_binary(old, target)
        if digest(target) != state['previous_binary_sha256'] or run([target, 'version']) != state['previous_binary_version']:
            raise RuntimeError('restored CLI did not match retained evidence')
    else:
        target.unlink(missing_ok=True)
    receipt = target.parent / '.quality-review-installations' / (host + '.json')
    receipt.unlink(missing_ok=True)
    state['status'] = 'ROLLED_BACK'
    write_json(directory / 'transaction.json', state)
    print('INSTALL_STATUS=ROLLED_BACK')


def download(url, target):
    run(['curl', '-fsSL', '--retry', '3', '--retry-delay', '2', url, '-o', target])


def checksum(path, name):
    values = [line.split()[0] for line in path.read_text().splitlines() if len(line.split()) == 2 and line.split()[1] == name]
    if len(values) != 1 or not re.fullmatch('[0-9a-fA-F]{64}', values[0]):
        raise RuntimeError('missing, duplicate, or invalid release checksum: ' + name)
    return values[0].lower()


def bootstrap(version, host):
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:[.-][A-Za-z0-9.-]+)?', version) or host not in ('codex', 'claude'):
        raise RuntimeError('usage: bootstrap.sh <vX.Y.Z> <codex|claude>')
    for executable in (host, 'curl', 'sh'):
        if shutil.which(executable) is None:
            raise RuntimeError(executable + ' is not installed; nothing was changed')
    capabilities = host_capabilities(host)
    validate_removal_scope(host, host_state(host, strict=True), capabilities)
    install_dir = Path(os.environ.get('INSTALL_DIR', str(Path.home() / '.local/bin'))).resolve()
    install_dir.mkdir(parents=True, exist_ok=True)
    target = install_dir / 'quality-review'
    if target.is_symlink() or (target.exists() and not target.is_file()):
        raise RuntimeError('refusing to replace a non-regular CLI')
    lock = install_dir / '.quality-review-upgrade.lock'
    try:
        lock.mkdir(mode=0o700)
    except FileExistsError:
        raise RuntimeError('another upgrade is active, or its lock requires inspection: ' + str(lock))
    backup = None
    changed = False
    state = None
    try:
        # The lock, rather than the earlier capability check, defines this transaction.
        before = host_state(host, strict=True)
        validate_removal_scope(host, before, capabilities)
        receipts = install_dir / '.quality-review-installations'
        private_dir(receipts)
        receipt = receipts / (host + '.json')
        if receipt.is_symlink():
            raise RuntimeError('installation receipt must not be a symlink')
        if receipt.is_file():
            previous = json.loads(receipt.read_text())
            if previous.get('version') == version and target.is_file() and digest(target) == previous.get('binary_sha256') and run([target, 'version']) == 'quality-review ' + version:
                actual = verify_plugin(host, before, version[1:])
                if actual == previous.get('plugin_sha256'):
                    print('INSTALL_STATUS=UNCHANGED')
                    print('QUALITY_REVIEW_BIN=' + str(target))
                    print('QUALITY_REVIEW_HOST=' + ('claude-code' if host == 'claude' else host))
                    print('QUALITY_REVIEW_ROLLBACK=' + previous['rollback'])
                    return
        root = install_dir / '.quality-review-rollbacks'
        private_dir(root)
        backup = Path(tempfile.mkdtemp(prefix='bootstrap.', dir=root))
        os.chmod(backup, 0o700)
        state = {'schema_version': 1, 'host': host, 'host_capabilities': capabilities, 'binary': str(target), 'requested_version': version, 'before': before, 'status': 'PREPARING'}
        if target.exists():
            state['previous_binary_version'] = run([target, 'version'])
            shutil.copy2(target, backup / 'quality-review.previous')
            state['previous_binary_sha256'] = digest(backup / 'quality-review.previous')
            if run([backup / 'quality-review.previous', 'version']) != state['previous_binary_version']:
                raise RuntimeError('installed CLI changed while its rollback snapshot was being prepared')
        state['previous_marketplace_sha256'] = marketplace_snapshot(host, before, backup / 'marketplace.previous')
        if before['plugin']:
            state['previous_plugin_sha256'] = verify_plugin(host, before, before['plugin']['version'])
            if tree_digest(backup / 'marketplace.previous/plugins/code-quality') != state['previous_plugin_sha256']:
                raise RuntimeError('installed plugin changed while its rollback snapshot was being prepared')
        shutil.copyfile(__file__, backup / 'transaction.py')
        (backup / 'rollback.sh').write_text('#!/bin/sh\nset -eu\nroot=$(CDPATH= cd "$(dirname "$0")" && pwd -P)\nexec python3 "$root/transaction.py" --rollback "$root"\n')
        os.chmod(backup / 'rollback.sh', 0o700)
        write_json(backup / 'transaction.json', state)
        sync_tree(backup)
        release_base = os.environ.get('QUALITY_REVIEW_RELEASE_BASE', 'https://github.com/Fueav/code-quality/releases/download/' + version)
        state['release_source'] = release_base
        with tempfile.TemporaryDirectory(prefix='stage.', dir=backup) as temporary:
            stage = Path(temporary)
            download(release_base + '/checksums.txt', stage / 'checksums.txt')
            download(release_base + '/install.sh', stage / 'install.sh')
            if digest(stage / 'install.sh') != checksum(stage / 'checksums.txt', 'install.sh'):
                raise RuntimeError('release installer checksum mismatch')
            environment = dict(os.environ, INSTALL_DIR=str(stage / 'bin'), QUALITY_REVIEW_RELEASE_BASE=release_base)
            run(['sh', stage / 'install.sh', version], env=environment)
            staged = stage / 'bin/quality-review'
            if run([staged, 'version']) != 'quality-review ' + version:
                raise RuntimeError('staged CLI version mismatch')
            state['new_binary_sha256'] = digest(staged)
            if host_state(host, strict=True) != before:
                raise RuntimeError('host plugin state changed while the upgrade was being prepared')
            if host_capabilities(host) != capabilities:
                raise RuntimeError('host capabilities changed while the upgrade was being prepared')
            validate_removal_scope(host, before, capabilities)
            current_digest = digest(target) if target.is_file() and not target.is_symlink() else None
            if target.is_symlink() or (target.exists() and not target.is_file()) or current_digest != state.get('previous_binary_sha256'):
                raise RuntimeError('installed CLI changed while the upgrade was being prepared')
            if before['plugin'] and verify_plugin(host, before, before['plugin']['version']) != state['previous_plugin_sha256']:
                raise RuntimeError('installed plugin changed while the upgrade was being prepared')
            state['status'] = 'SWITCHING'
            write_json(backup / 'transaction.json', state)
            changed = True  # A command can mutate before returning failure; always compensate.
            remove_current(host, before, capabilities)
            source = 'Fueav/code-quality' if host == 'codex' else REPOSITORY + '#' + version
            add_plugin(host, source, version)
            installed = host_state(host)
            plugin_sha = verify_plugin(host, installed, version[1:])
            replace_binary(staged, target)
            if digest(target) != state['new_binary_sha256'] or run([target, 'version']) != 'quality-review ' + version:
                raise RuntimeError('activated CLI version or digest mismatch')
        state['status'] = 'INSTALLED'
        write_json(backup / 'transaction.json', state)
        write_json(receipt, {'schema_version': 1, 'version': version, 'binary_sha256': digest(target), 'plugin_sha256': plugin_sha, 'rollback': str(backup / 'rollback.sh')})
        print('INSTALL_STATUS=INSTALLED')
        print('QUALITY_REVIEW_BIN=' + str(target))
        print('QUALITY_REVIEW_HOST=' + ('claude-code' if host == 'claude' else host))
        print('QUALITY_REVIEW_ROLLBACK=' + str(backup / 'rollback.sh'))
        print('NEXT_COMMAND=' + shlex.quote(str(target)) + ' doctor --host ' + ('claude-code' if host == 'claude' else host) + ' --repo .')
        if host == 'claude':
            print('CLAUDE_RELOAD=/reload-plugins')
    except BaseException:
        if changed:
            try:
                rollback(backup, active=True)
            except Exception as failure:
                print('ROLLBACK_REQUIRED=' + str(backup / 'rollback.sh'), file=sys.stderr)
                print('rollback could not complete: ' + str(failure), file=sys.stderr)
        if backup is not None:
            print('QUALITY_REVIEW_ROLLBACK=' + str(backup / 'rollback.sh'), file=sys.stderr)
        raise
    finally:
        lock.rmdir()


def interrupted(signum, frame):
    raise KeyboardInterrupt('installation interrupted; inspect retained rollback evidence')


signal.signal(signal.SIGTERM, interrupted)
signal.signal(signal.SIGHUP, interrupted)
try:
    if sys.version_info < (3, 8):
        raise RuntimeError('Python 3.8 or newer is required; nothing was changed')
    if len(sys.argv) == 3 and sys.argv[1] == '--rollback':
        directory = Path(sys.argv[2]).resolve()
        state = json.loads((directory / 'transaction.json').read_text())
        lock = Path(state['binary']).parent / '.quality-review-upgrade.lock'
        try:
            lock.mkdir(mode=0o700)
        except FileExistsError:
            raise RuntimeError('another upgrade is active; rollback cannot proceed')
        try:
            rollback(directory)
        finally:
            lock.rmdir()
    elif len(sys.argv) == 3:
        bootstrap(sys.argv[1], sys.argv[2])
    else:
        raise RuntimeError('usage: bootstrap.sh <vX.Y.Z> <codex|claude>')
except (Exception, KeyboardInterrupt) as failure:
    print('quality-review installation failed: ' + str(failure), file=sys.stderr)
    sys.exit(1)
PY
python3 "$program_dir/bootstrap.py" "$@"

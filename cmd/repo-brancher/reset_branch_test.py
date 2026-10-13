#!/usr/bin/env python3
"""Offline recovery checks: run python3 cmd/repo-brancher/reset_branch_test.py.

GitHub and pushes are mocked; URL and credential probes use real Git without network.
"""
import copy
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
from urllib.parse import quote

SCRIPT = Path(os.environ.get('TEST_SCRIPT', str(Path(__file__).with_name('reset-branch.sh')))).read_text()
SNAPSHOT = {'url': 'https://api.github.com/repos/example/component/branches/release-next/protection',
 'required_status_checks': {'strict': False,
                            'contexts': ['ci/test'],
                            'checks': [{'context': 'ci/test', 'app_id': 1234}]},
 'enforce_admins': {'enabled': False},
 'required_pull_request_reviews': None,
 'restrictions': None,
 'required_signatures': {'enabled': False},
 'required_linear_history': {'enabled': False},
 'allow_force_pushes': {'enabled': False},
 'allow_deletions': {'enabled': False},
 'block_creations': {'enabled': False},
 'required_conversation_resolution': {'enabled': False},
 'lock_branch': {'enabled': False},
 'allow_fork_syncing': {'enabled': False}}
OLD = '1' * 40
NEW = '2' * 40
CHANGED = 'a' * 40

MOCK = r'''#!/usr/bin/python3
import json, os, sys
from pathlib import Path
from urllib.parse import quote
state_path = Path(os.environ['TEST_STATE'])
s = json.loads(state_path.read_text())
args = sys.argv[1:]
name = Path(sys.argv[0]).name
event = {'tool': name, 'args': args}
s['events'].append(event)
def finish(code=0, output=''):
    state_path.write_text(json.dumps(s))
    if output:
        print(output)
    sys.exit(code)
if name == 'git':
    if args[0] == 'check-ref-format':
        import subprocess
        finish(subprocess.call([os.environ['TEST_REAL_GIT'], *args]))
    assert os.environ.get('GIT_CONFIG_GLOBAL') == '/dev/null'
    assert os.environ.get('GIT_CONFIG_NOSYSTEM') == '1'
    assert os.environ.get('GIT_CONFIG_COUNT') == '0'
    for i, arg in enumerate(args):
        if arg == '-C' or arg == '-c':
            continue
        if arg in ('init', 'fetch', 'rev-parse', 'push'):
            cmd = arg
            rest = args[i+1:]
            break
    if cmd == 'rev-parse':
        finish(output=s['new'])
    if cmd == 'fetch':
        assert rest[-2] == 'https://github.com/' + s['repo'] + '.git'
        assert rest[-1] == 'refs/heads/' + s['src']
    if cmd == 'push':
        assert rest[-2] == 'https://github.com/' + s['repo'] + '.git'
        assert '--force-with-lease=refs/heads/' + s['dst'] + ':' + s['old'] in rest
        assert rest[-1] == s['new'] + ':refs/heads/' + s['dst']
        if '--dry-run' in rest:
            if s['mode'] == 'preflight-fail': finish(1)
            finish()
        assert not s['protected'], 'Protection must be removed for actual push'
        if s['mode'] == 'concurrent':
            s['sha'] = 'a' * 40
            finish(1)
        if s['mode'] == 'push-fail': finish(1)
        s['sha'] = s['new']
    finish()
assert args[0] == 'api'
assert os.environ['GH_TOKEN'] == s['expected_token']
endpoint = next(a for a in args if a.startswith('repos/'))
assert endpoint.startswith('repos/' + s['repo'])
dst_api = quote(s['dst'], safe='')
method = args[args.index('-X')+1] if '-X' in args else 'GET'
event['method'] = method
event['endpoint'] = endpoint
if endpoint.endswith('/git/refs'):
    assert method == 'POST'
    assert 'sha=' + s['old'] in args
    assert any(a.startswith('ref=refs/heads/backup/' + s['dst'] + '-') for a in args)
    s['backup'] = True
    finish(output='{}')
if '/git/ref/heads/' in endpoint:
    assert endpoint == 'repos/' + s['repo'] + '/git/ref/heads/' + dst_api
    finish(output=s['sha'])
if endpoint.endswith('/required_signatures'):
    assert method == 'POST'
    s['snapshot']['required_signatures']['enabled'] = True
    finish()
if endpoint.endswith('/protection'):
    assert endpoint == 'repos/' + s['repo'] + '/branches/' + dst_api + '/protection'
    if method == 'GET':
        if not s['protected']: finish(1)
        finish(output=json.dumps(s['snapshot']))
    if method == 'DELETE':
        assert s.get('backup')
        s['protected'] = False
        if s['mode'] == 'delete-response-lost': finish(1)
        finish()
    assert method == 'PUT'
    if s['mode'] == 'restore-fail': finish(1)
    payload = json.loads(Path(args[args.index('--input')+1]).read_text())
    checks_request = payload['required_status_checks']
    if checks_request is not None:
        assert 'strict' in checks_request
        assert ('checks' in checks_request) != ('contexts' in checks_request), 'HTTP 422: checks and contexts must be mutually exclusive'
    event['payload'] = json.loads(json.dumps(payload))
    snap = {'url': s['snapshot']['url']}
    bools = ['enforce_admins', 'required_linear_history', 'allow_force_pushes',
             'allow_deletions', 'block_creations', 'required_conversation_resolution',
             'lock_branch', 'allow_fork_syncing']
    for key in bools:
        snap[key] = {'enabled': payload[key]}
    def actors(p):
        return {'users': [{'login': x} for x in reversed(p['users'])],
                'teams': [{'slug': x} for x in p['teams']],
                'apps': [{'slug': x} for x in p['apps']]}
    checks = payload['required_status_checks']
    snap['required_status_checks'] = checks
    if checks is not None:
        checks['contexts'] = [c['context'] for c in checks.get('checks', [])]
        for check in checks.get('checks', []):
            if check['app_id'] == -1: check['app_id'] = None
    reviews = payload['required_pull_request_reviews']
    snap['required_pull_request_reviews'] = reviews
    if reviews:
        for key in ['dismissal_restrictions', 'bypass_pull_request_allowances']:
            if key in reviews: reviews[key] = actors(reviews[key])
    snap['restrictions'] = actors(payload['restrictions']) if payload['restrictions'] else None
    snap['required_signatures'] = {'enabled': False}
    s['snapshot'] = snap
    s['protected'] = True
    finish()
if '/branches/' in endpoint:
    assert endpoint == 'repos/' + s['repo'] + '/branches/' + dst_api
    finish(output='true' if s['protected'] else 'false')
finish(output='false' if s['mode'] == 'no-admin' else 'true')
'''

def run(mode='success', *, protected=True, answer=None, fixture=None, restore_only=False,
        repo='example/component', src='main', dst='release-next',
        interactive=False, pat_flag=False, cached=True, raw_url=False, cache_owner=None,
        validation_args=None):
    if answer is None:
        answer = dst + '\n'
    with tempfile.TemporaryDirectory(prefix='github-bp-test-') as td:
        root = Path(td)
        bin_dir = root / 'bin'
        bin_dir.mkdir()
        for tool in ['git', 'gh']:
            p = bin_dir / tool
            p.write_text(MOCK)
            p.chmod(0o755)
        script = root / 'github-bp'
        script.write_text(SCRIPT)
        snap = copy.deepcopy(fixture or SNAPSHOT)
        branch_url = dst if raw_url else quote(dst, safe='')
        snap['url'] = 'https://api.github.com/repos/' + repo + '/branches/' + branch_url + '/protection'
        saved = root / (repo.split('/')[1] + '-' + quote(dst, safe='') + '-protection-20261013T115912Z.json')
        if (not protected and cached) or restore_only:
            saved.write_text(json.dumps(snap))
        if cache_owner:
            other = copy.deepcopy(snap)
            other['url'] = other['url'].replace('/repos/' + repo + '/', '/repos/' + cache_owner + '/' + repo.split('/')[1] + '/')
            saved.write_text(json.dumps(other))
        expected_token = 'test-pat-flag' if pat_flag else 'test-pat-never-a-real-token'
        state = root / 'state.json'
        state.write_text(json.dumps({'events': [], 'snapshot': snap, 'protected': protected,
                                     'old': OLD, 'sha': OLD, 'new': NEW, 'mode': mode,
                                     'repo': repo, 'src': src, 'dst': dst, 'expected_token': expected_token}))
        env = {**os.environ, 'GH_TOKEN': 'test-pat-never-a-real-token', 'TEST_STATE': str(state),
               'PATH': str(bin_dir) + ':' + os.environ['PATH'], 'TEST_REAL_GIT': shutil.which('git')}
        args = ['bash', str(script)]
        if restore_only:
            args += ['--restore-protection', str(saved)]
        elif interactive:
            env.pop('GH_TOKEN')
            answer = repo + '\n' + src + '\n' + dst + '\n' + expected_token + '\n' + answer
        else:
            args += ['--repo', repo, '--from', src, '--to', dst]
        if pat_flag: args += ['--pat', expected_token]
        if validation_args is not None: args = ['bash', str(script), *validation_args]
        result = subprocess.run(args, input=answer, text=True, capture_output=True, env=env, cwd=root, timeout=20)
        s = json.loads(state.read_text())
        mutations = [e for e in s['events'] if e.get('method') not in (None, 'GET')]
        expected_protection = protected or (cached and not cache_owner)
        if validation_args is not None:
            assert result.returncode == 2, result.stderr
            assert not mutations
            assert not any(e['tool'] == 'gh' for e in s['events'])
        elif mode == 'success' and (answer.startswith(dst) or interactive) and not restore_only:
            assert result.returncode == 0, result.stderr
            assert s['sha'] == NEW and s['protected'] == expected_protection and s['backup']
            if not expected_protection:
                assert not any(e.get('endpoint', '').endswith('/protection') for e in mutations)
            else:
                assert mutations[-1]['method'] in ('PUT', 'POST')
        elif restore_only:
            assert result.returncode == 0, result.stderr
            assert s['protected'] and s['sha'] == OLD
            assert not any(e['tool'] == 'git' and 'check-ref-format' not in e['args'] for e in s['events'])
        elif answer == 'cancel\n':
            assert result.returncode != 0
            assert s['protected'] and s['sha'] == OLD
            assert not any(e.get('method') == 'DELETE' for e in mutations)
        else:
            assert result.returncode != 0, result.stdout
            assert s['sha'] == (CHANGED if mode == 'concurrent' else NEW if mode == 'restore-fail' else OLD)
            assert s['protected'] == (expected_protection and mode != 'restore-fail')
            if mode in ('preflight-fail', 'no-admin'):
                assert not mutations
            if mode == 'restore-fail':
                assert 'restoration failed' in result.stderr
                assert list(root.glob('*-protection-*.json'))
        assert expected_token not in result.stdout + result.stderr
        print('PASS', mode, 'restore only' if restore_only else 'recover' if not protected else '')
        return mutations

run()
run('push-fail')
run('delete-response-lost')
run('concurrent')
run('preflight-fail')
run('no-admin')
run('restore-fail')
run(answer='cancel\n')
run(protected=False)
run(protected=False, answer='cancel\n')
run(protected=False, restore_only=True)

rich = copy.deepcopy(SNAPSHOT)
actors = {'users': [{'login': 'b'}, {'login': 'a'}], 'teams': [{'slug': 'team'}], 'apps': [{'slug': 'app'}]}
rich['required_pull_request_reviews'] = {
    'dismiss_stale_reviews': True, 'require_code_owner_reviews': True,
    'required_approving_review_count': 2, 'require_last_push_approval': True,
    'dismissal_restrictions': actors, 'bypass_pull_request_allowances': actors}
rich['restrictions'] = actors
rich['required_signatures']['enabled'] = True
rich['required_status_checks']['checks'][0]['app_id'] = None
mutations = run(fixture=rich)
payload = next(e['payload'] for e in mutations if e.get('method') == 'PUT')
assert payload['required_pull_request_reviews']['dismissal_restrictions']['users'] == ['a', 'b']
assert payload['required_pull_request_reviews']['bypass_pull_request_allowances']['apps'] == ['app']
assert payload['required_status_checks']['checks'][0]['app_id'] == -1
print('All 12 reset/recovery scenarios passed.')

legacy = copy.deepcopy(SNAPSHOT)
del legacy['required_status_checks']['checks']
mutations = run(fixture=legacy)
payload = next(e['payload'] for e in mutations if e.get('method') == 'PUT')
assert payload['required_status_checks'] == {'strict': False, 'checks': [{'context': 'ci/test', 'app_id': -1}]}
no_checks = copy.deepcopy(SNAPSHOT)
no_checks['required_status_checks'] = None
run(fixture=no_checks)
print('PASS legacy contexts-only and disabled-checks snapshots')

run(repo='another/project', src='main', dst='release/v2', pat_flag=True)
run(repo='another/project', src='dev/new', dst='release/v2', raw_url=True)
run(repo='another/project', src='main', dst='release/v2', protected=False)
run(repo='another/project', src='main', dst='release/v2', protected=False, restore_only=True)
run(repo='another/project', src='main', dst='release/v2', interactive=True)
run(repo='another/project', src='main', dst='release/v2', protected=False, cached=False)
run(repo='another/project', src='main', dst='release/v2', protected=False, cache_owner='unrelated')
run(repo='another/project', src='main', dst='release/v2', protected=False, cached=False, mode='push-fail')
run(repo='another/project', src='dev/zażółć', dst='release/żółć')
run(validation_args=['--repo'])
run(validation_args=['--repo', 'bad/repo/name', '--from', 'main', '--to', 'release'])
run(validation_args=['--repo', 'another/project', '--from', 'main', '--to', 'main'])
run(validation_args=['--repo', 'another/project', '--from', 'main', '--to', '../bad'])
run(validation_args=['--unknown'])
print('PASS configurable repository/branches/PAT, prompts, slash/unicode branches, snapshot isolation, and input validation')

# Exercise the actual Git wrapper without network or real credentials.
with tempfile.TemporaryDirectory(prefix='github-bp-url-') as td:
    root = Path(td)
    config = root / 'global.gitconfig'
    config.write_text('[url "git@github.com:"]\n    insteadOf = https://github.com/\n    pushInsteadOf = https://github.com/\n')
    wrapper_start = SCRIPT.index('g() {')
    wrapper = SCRIPT[wrapper_start:SCRIPT.index('\nif ((restore_only))', wrapper_start)]
    probe = root / 'probe.sh'
    probe.write_text('set -euo pipefail\ntmp=$1\n' + wrapper + '''
g init -q
g ls-remote --get-url https://github.com/example/component.git
printf 'protocol=https\nhost=github.com\n\n' | g credential fill
''')
    env = {**os.environ, 'GH_TOKEN': 'test-pat-never-a-real-token', 'GIT_CONFIG_GLOBAL': str(config)}
    raw = subprocess.check_output(['git', 'ls-remote', '--get-url', 'https://github.com/example/component.git'], env=env, text=True)
    assert raw.strip() == 'git@github.com:example/component.git'
    fixed = subprocess.check_output(['bash', str(probe), td], env=env, text=True)
    assert fixed.splitlines()[0] == 'https://github.com/example/component.git'
    assert 'username=x-access-token' in fixed
    assert 'password=test-pat-never-a-real-token' in fixed
    print('PASS actual Git HTTPS rewrite isolation and PAT credential helper')

"""Three approved existing Network PostgreSQL cases; isolated fixture only."""
from pathlib import Path
import argparse, datetime, fcntl, hashlib, json, os, re, signal, subprocess, tarfile, time, urllib.parse

R = Path('/home/chabking/workspace/ani-resource-service-runs/vm-mvp-20261023-01a168ce')
D = R / 'legacy-network-pg-01'
GO = '/home/chabking/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.10.linux-amd64/bin/go'

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def save(path, value):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump(value, out, indent=2); out.write('\n'); out.flush(); os.fsync(out.fileno())

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute-authorized', action='store_true', required=True)
    parser.add_argument('--plan-sha', required=True)
    args = parser.parse_args()
    assert os.environ.get('SSH_CONNECTION', '').split()[2:3] == ['172.16.101.31']
    pf = D / 'prepared-oci.json'
    assert sha(pf) == args.plan_sha
    plan = json.loads(pf.read_text())
    assert sha(__file__) == plan['runner_sha256']
    lock = open('/home/chabking/workspace/ani-network-service-runs/net05a-heavy.lock', 'a')
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        raise SystemExit(73)
    output = D / 'executed-oci.json'
    assert not output.exists(), 'preserve existing attempt; never repeat automatically'
    started = time.monotonic()
    result = {'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'plan_sha256': args.plan_sha, 'runner_sha256': sha(__file__),
              'source_commit': plan['source_commit'], 'source_tree': plan['source_tree'],
              'application_calls': 0, 'candidate_changes': 0, 'cluster_writes': 0,
              'force_or_connection_termination': False, 'named_results': [], 'fixtures': []}
    phase = 'fixture-guards'
    try:
        fixture = json.loads((R / 'evidence/pg-fixture.json').read_text())
        cid = (R / 'private/pg/container-id').read_text().strip()
        assert cid == fixture['container_id'] and fixture['label'] == 'ani.vm.fixture=vm-mvp-20261023-01a168ce'
        actual = json.loads(subprocess.check_output(['docker', 'inspect', cid], text=True))[0]
        assert actual['State']['Running'] and actual['Config']['Labels']['ani.vm.fixture'] == 'vm-mvp-20261023-01a168ce'
        assert any(p['HostIp'] == '127.0.0.1' and p['HostPort'] == '33075' for p in actual['NetworkSettings']['Ports']['5432/tcp'])
        adminref = R / 'private/pg/admin.dsn'
        st = adminref.stat()
        assert st.st_uid == os.getuid() and st.st_mode & 0o077 == 0 and not adminref.is_symlink()
        admintext = adminref.read_text().strip()
        admin = urllib.parse.urlsplit(admintext)
        assert admin.hostname == '127.0.0.1' and admin.port == 33075 and admin.path == '/postgres' and admin.username == 'postgres'
        result['postgres'] = {'container_id': cid, 'label': fixture['label'], 'admin_reference': str(adminref), 'host': '127.0.0.1', 'port': 33075}
        phase = 'immutable-source'
        assert sha(D / 'resource-oci-tests.tar.gz') == plan['archive_sha256'] and sha(D / 'source-manifest-oci.json') == plan['source_manifest_sha256']
        source = D / 'source-oci'
        assert not source.exists()
        source.mkdir(mode=0o700)
        with tarfile.open(D / 'resource-oci-tests.tar.gz') as archive:
            archive.extractall(source, filter='data')
        expected = json.loads((D / 'source-manifest-oci.json').read_text())
        before = {str(p.relative_to(source)): sha(p) for p in source.rglob('*') if p.is_file()}
        assert before == expected and len(before) == 558
        overlay = plan['overlay']
        helper = source / overlay['path']
        assert sha(helper) == overlay['original_sha256'] and sha(D / 'postgres-overlay.go.txt') == overlay['input_sha256']
        helper.write_bytes((D / 'postgres-overlay.go.txt').read_bytes())
        subprocess.run([str(Path(GO).parent / 'gofmt'), '-w', str(helper)], check=True, timeout=10)
        after = {str(p.relative_to(source)): sha(p) for p in source.rglob('*') if p.is_file()}
        assert set(after) == set(before) and {k for k in after if after[k] != before[k]} == {overlay['path']}
        assert 'WITH (FORCE)' not in helper.read_text() and 'pg_terminate_backend' not in helper.read_text()
        result['overlay'] = {**overlay, 'formatted_sha256': sha(helper)}
        result['source'] = {'path': str(source), 'files': len(after), 'only_helper_overlay_changed': True}
        tmp = D / 'go-tmp-oci'
        tmp.mkdir(mode=0o700)
        assert tmp.stat().st_uid == os.getuid() and tmp.stat().st_mode & 0o777 == 0o700
        private = R / 'private/legacy-network-pg-oci'
        private.mkdir(mode=0o700)
        assert private.stat().st_uid == os.getuid() and private.stat().st_mode & 0o777 == 0o700
        log = private / 'tests.jsonl'
        assert not log.exists()
        env = {k: v for k, v in os.environ.items() if k in ('HOME', 'USER', 'LOGNAME', 'LANG', 'LC_ALL', 'TZ')}
        env.update(PATH=str(Path(GO).parent) + ':/usr/bin:/bin', GOTOOLCHAIN='local', GOWORK='off', GOMAXPROCS='2',
                   GOFLAGS='-p=2 -mod=readonly', GOMEMLIMIT='1536MiB', GOMODCACHE=str(R / 'cache/go-mod'),
                   GOCACHE=str(R / 'cache/go-build'), GOPROXY='off', GOTMPDIR=str(tmp), NETWORK_TEST_ADMIN_DSN=admintext)
        phase = 'zero-db-input-check'
        listing = subprocess.run([GO, 'list', '-e', '-test', '-deps', '-json', './internal/data/network'], cwd=source, env={k:v for k,v in env.items() if k != 'NETWORK_TEST_ADMIN_DSN'}, text=True, capture_output=True, timeout=60)
        save(private / 'go-list.json', {'stdout':listing.stdout, 'stderr':listing.stderr, 'exit_code':listing.returncode})
        decoder = json.JSONDecoder(); remaining = listing.stdout; errors = []; packages = 0
        while remaining.strip():
            item, consumed = decoder.raw_decode(remaining.lstrip()); remaining = remaining.lstrip()[consumed:]; packages += 1
            for error in ([item['Error']] if item.get('Error') else []) + item.get('DepsErrors', []):
                errors.append({'import_path':item.get('ImportPath'), 'position':error.get('Pos'), 'error':error.get('Err')})
        result['input_check'] = {'command':[GO,'list','-e','-test','-deps','-json','./internal/data/network'], 'exit_code':listing.returncode, 'packages':packages, 'errors':errors, 'business_or_pg_calls':0}
        assert listing.returncode == 0 and packages > 0 and not errors, 'repository import/embed input check failed before any test'
        assert {str(p.relative_to(source)):sha(p) for p in source.rglob('*') if p.is_file()} == after
        command = [GO, 'test', '-v', '-json', './internal/data/network', '-count=1', '-timeout=180s', '-run', '^(' + '|'.join(plan['tests']) + ')$']
        result['command'] = command; result['environment'] = {k: v for k, v in env.items() if k not in ('NETWORK_TEST_ADMIN_DSN', 'HOME', 'USER', 'LOGNAME')}
        phase = 'three-real-pg-cases'
        print('RUN three fixed Network PG cases; immutable8589 source + normal-cleanup helper only', flush=True)
        fd = os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as out:
            proc = subprocess.Popen(command, cwd=source, env=env, stdout=out, stderr=subprocess.STDOUT, start_new_session=True)
            result['owned_process_pid'] = proc.pid
            save(output, result)
            try:
                code = proc.wait(timeout=240)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGTERM); proc.wait(timeout=10)
                raise RuntimeError('owned test process budget expired; fixture retained')
        result['exit_code'] = code
        fixture_events = []
        for line in log.read_text().splitlines():
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            if event.get('Test') and event.get('Action') in ('pass', 'fail', 'skip'):
                result['named_results'].append({k: event[k] for k in ('Package', 'Test', 'Action', 'Elapsed') if k in event})
            match = re.search(r'PG_FIXTURE_(ROLE|DATABASE) (created|dropped) name=(network_(?:test|owner|runtime)_[a-f0-9]{32}) oid=(\d+)(?: owner=(network_owner_[a-f0-9]{32}) owner_oid=(\d+))?', event.get('Output', ''))
            if match:
                kind, action, name, oid, owner, owner_oid = match.groups()
                fixture_events.append({'kind': kind, 'action': action, 'name': name, 'oid': int(oid), **({'owner': owner, 'owner_oid': int(owner_oid)} if owner else {})})
        result['fixture_events'] = fixture_events
        result['private_log_reference'] = str(log)
        assert code == 0 and len(result['named_results']) == 3 and all(any(e['Test'] == n and e['Action'] == 'pass' for e in result['named_results']) for n in plan['tests'])
        assert not any(e['Action'] in ('fail', 'skip') for e in result['named_results'])
        created = [e for e in fixture_events if e['action'] == 'created']
        assert len(created) == 9 and sum(e['kind'] == 'DATABASE' for e in created) == 3
        phase = 'exact-oid-absence'
        for original in created:
            assert any(e['action'] == 'dropped' and all(e[k] == original[k] for k in ('kind', 'name', 'oid')) for e in fixture_events)
            table, key = ('pg_database', 'datname') if original['kind'] == 'DATABASE' else ('pg_roles', 'rolname')
            query = f"SELECT count(*) FROM {table} WHERE {key}='{original['name']}' OR oid={original['oid']};"
            q = subprocess.run(['docker', 'exec', '-i', cid, 'psql', '-X', '-A', '-t', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', 'postgres'], input=query, text=True, capture_output=True, timeout=10)
            assert q.returncode == 0 and q.stdout.strip() == '0', 'exact original fixture not absent'
            result['fixtures'].append({**original, 'normal_drop_logged': True, 'current_name_and_oid_absent': True})
        assert {str(p.relative_to(source)): sha(p) for p in source.rglob('*') if p.is_file()} == after
        result['result'] = 'PASS_THREE_REAL_PG_ZERO_SKIP_NORMAL_EXACT_OID_CLEANUP'
    except Exception as error:
        result.update(result='INCOMPLETE_NO_AUTOMATIC_RETRY', failure_type=type(error).__name__, failed_phase=phase)
    finally:
        result['seconds'] = round(time.monotonic() - started, 3)
        result['finished_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        save(output, result)
        print(json.dumps({k: result.get(k) for k in ('result', 'seconds', 'failed_phase', 'named_results')}) + '\nreceipt=' + str(output), flush=True)
        fcntl.flock(lock, fcntl.LOCK_UN)

if __name__ == '__main__':
    main()

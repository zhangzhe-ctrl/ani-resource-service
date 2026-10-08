#!/usr/bin/env python3
"""Bounded real-Harbor technical probe; never creates a Pod or claims product acceptance."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import signal
import ssl
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

MAX_JSON = 1024 * 1024


class Stop(Exception):
    pass


def require(ok, message):
    if not ok:
        raise Stop(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate JSON key')
        result[key] = value
    return result


def decode(data):
    try:
        return json.loads(data, object_pairs_hook=unique_object)
    except (ValueError, UnicodeError):
        raise Stop('invalid JSON') from None


def private_bytes(name, limit=65536):
    path = Path(name)
    require(path.is_absolute(), 'absolute private input required')
    before = path.lstat()
    require(stat.S_ISREG(before.st_mode) and not before.st_mode & 0o077 and before.st_size <= limit,
            'private regular input required')
    with path.open('rb') as f:
        after = os.fstat(f.fileno())
        require((before.st_dev, before.st_ino) == (after.st_dev, after.st_ino)
                and not after.st_mode & 0o077, 'private input changed')
        data = f.read(limit + 1)
    require(0 < len(data) <= limit, 'private input exceeds limit')
    return data


def new_private(path, data):
    with open(path, 'xb', opener=lambda p, f: os.open(p, f, 0o600)) as out:
        out.write(data)
        out.flush()
        os.fsync(out.fileno())


def origin(value):
    u = urllib.parse.urlsplit(value)
    require(u.scheme == 'https' and u.netloc and not u.username and not u.password
            and u.path in ('', '/') and not u.query and not u.fragment, 'HTTPS origin required')
    return 'https://' + u.netloc


def canonical_uuid(value):
    try:
        return str(uuid.UUID(value)) == value and uuid.UUID(value).int != 0
    except (ValueError, TypeError, AttributeError):
        return False


def validate_profile(p):
    require(p.get('format') == 'image-mvp-live-profile/v1' and p.get('approved') is True
            and isinstance(p.get('approval_reference'), str) and p['approval_reference'].strip(),
            'blocked: approved live profile with approval reference required')
    run = p.get('run_id', '')
    require(re.fullmatch(r'[a-z][a-z0-9-]{7,31}', run), 'bounded lowercase run ID required')
    h, c, s, budget = p['harbor'], p['cluster'], p['smoke'], p['resource_budget']
    require(origin(h['management_url']) == 'https://' + h['registry_authority'], 'Harbor authority mismatch')
    origin(s['governance_url']); origin(c['api_server'])
    require(s['execution_host'] == socket.gethostname(), 'profile execution host mismatch')
    require(s['allow_technical_runtime_read'] is True and canonical_uuid(s['cluster_uid']),
            'technical runtime read and cluster UID must be approved')
    require(h['allow_existing_platform_write'] is False and h['actual_version'], 'fresh test platform required')
    require(p['cleanup']['require_uid_and_owner_match'] is True
            and p['cleanup']['global_gc_allowed'] is False, 'unsafe cleanup policy')
    require(budget['go_cpu_quota_percent'] <= 200 and budget['go_memory_max'] == '2300M'
            and budget['go_swap_max'] == 0 and 0 < s['timeout_seconds'] <= 1200
            and 0 < s['max_image_bytes'] <= 32 * 1024 * 1024, 'budget exceeds approved envelope')
    require(re.fullmatch(r'[0-9a-f]{40}', s['resource_sha'])
            and re.fullmatch(r'[0-9a-f]{40}', s['governance_sha']), 'full source SHAs required')
    require(re.fullmatch(r'[^\s@]+/[^\s@]+@sha256:[0-9a-f]{64}', p['base_image_digest'])
            and '://' not in p['base_image_digest'], 'approved immutable base image required')
    tenants = p['test_tenants']
    require(len(tenants) == 2 and len({t['tenant_id'] for t in tenants}) == 2, 'two distinct tenants required')
    names = [s['platform_project']]
    for i, t in enumerate(tenants):
        require(canonical_uuid(t['tenant_id']) and t['slug'] == run + '-' + 'ab'[i]
                and t['project_name'] == 't-' + t['slug'], 'tenant scope must be exact and run-owned')
        names.append(t['project_name'])
    require(s['platform_project'] == run + '-platform' and set(h['allowed_project_names']) == set(names)
            and len(h['allowed_project_names']) == 3, 'exact three-project allowlist required')
    require(len(p['user_credential_files']) == 2, 'two private JWT files required')
    require(c['allowed_namespaces'] and c['context_name'], 'approved cluster scope required')
    require(re.fullmatch(r'[0-9a-f]{64}', c['ca_sha256']), 'cluster CA fingerprint required')
    return p


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Probe:
    def __init__(self, profile, profile_path, work):
        self.p, self.s, self.h = profile, profile['smoke'], profile['harbor']
        self.profile_path, self.work = str(profile_path), work
        self.deadline = time.monotonic() + self.s['timeout_seconds']
        self.stage = 'preflight'
        self.private = work / 'private'
        self.events = work / 'events.jsonl'
        self.spaces, self.images, self.auths = [], [], []
        self.env = dict(os.environ, REGISTRY_AUTH_FILE=str(self.private / 'anonymous.json'))

    def remaining(self, maximum=60):
        left = self.deadline - time.monotonic()
        require(left > 0, 'probe deadline exceeded')
        return min(left, maximum)

    def event(self, kind, **facts):
        # Call sites pass only allowlisted non-secret facts, never dependency bodies.
        record = dict(time=time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), stage=self.stage,
                      kind=kind, **facts)
        with self.events.open('a', encoding='utf-8') as out:
            out.write(json.dumps(record, sort_keys=True) + '\n')
            out.flush(); os.fsync(out.fileno())

    def command(self, label, args, body=None, timeout=60):
        self.event('command-start', command=label)
        # No shell, inherited registry auth, raw subprocess output, or secret argv.
        r = subprocess.run(args, input=body, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                           timeout=self.remaining(timeout), env=self.env, check=False)
        self.event('command-exit', command=label, exit_code=r.returncode)
        require(r.returncode == 0, label + ' failed; inspect private environment, not secret output')
        require(len(r.stdout) <= MAX_JSON, label + ' output too large')
        return r.stdout

    def http(self, root, ca, method, path, body=None, headers=None):
        require(path.startswith('/') and not path.startswith('//'), 'relative API path required')
        context = ssl.create_default_context(cafile=ca)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
                                            urllib.request.HTTPSHandler(context=context))
        req = urllib.request.Request(origin(root) + path, method=method, headers=headers or {}, data=body)
        try:
            response = opener.open(req, timeout=self.remaining(45))
        except urllib.error.HTTPError as e:
            response = e
        with response:
            result = response.read(MAX_JSON + 1)
            require(len(result) <= MAX_JSON, 'HTTP body exceeds bound')
            return response.status, response.headers, result

    def gov(self, index, method, path, body=None, expected=200):
        token = private_bytes(self.p['user_credential_files'][index]).decode().strip()
        require(token and '\n' not in token and '\r' not in token, 'invalid private JWT file')
        headers = {'Authorization': 'Bearer ' + token}
        if body is not None:
            headers['Content-Type'] = 'application/json'
        status, reply_headers, raw = self.http(self.s['governance_url'], self.s['governance_ca_file'],
                                             method, '/api/v1/images' + path,
                                             json.dumps(body).encode() if body is not None else None, headers)
        self.event('governance', tenant_index=index, method=method, path=path, status=status)
        require(status == expected, 'Governance status mismatch; durable request may require recovery')
        if 'publisher-credential' in path:
            require('no-store' in reply_headers.get('Cache-Control', ''), 'credential response may be cached')
        return decode(raw) if raw else {}

    def admin(self, action, body, secret=None):
        self.event('admin-intent', action=action, idempotency_key=body.get('idempotency_key'))
        args = [self.s['resource_binary'], '-conf', self.s['runtime_config'],
                '-image-operator-config', self.s['operator_config'], '-image-admin', action]
        if secret:
            args += ['-image-secret-output', str(secret)]
        return decode(self.command('image-admin/' + action, args, json.dumps(body).encode()))['result']

    def key(self, operation):
        return self.p['run_id'] + '-' + operation

    def auth_file(self, name, username, secret):
        require(username and secret and isinstance(username, str) and isinstance(secret, str), 'missing delivered credential')
        path = self.private / (name + '.json')
        auth = base64.b64encode((username + ':' + secret).encode()).decode()
        new_private(path, json.dumps({'auths': {self.h['registry_authority']: {'auth': auth}}}).encode())
        return path

    def inspect(self, reference, auth, destination=True):
        args = ['skopeo', 'inspect', '--tls-verify=true', '--raw', '--authfile', str(auth)]
        if destination:
            args += ['--cert-dir', str(self.private / 'certs')]
        return self.command('registry-inspect', args + ['docker://' + reference])

    def registry_get(self, reference, auth):
        # Registry challenge handling is constrained to the approved Harbor origin.
        path = reference.split('/', 1)[1]
        repository, digest = path.split('@', 1)
        endpoint = '/v2/' + repository + '/manifests/' + digest
        headers = {'Accept': 'application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json'}
        status, response_headers, body = self.http(self.h['management_url'], self.h['ca_file'], 'GET', endpoint, headers=headers)
        require(status == 401, 'private registry must challenge anonymous access')
        challenge = response_headers.get('WWW-Authenticate', '')
        require(challenge.lower().startswith('bearer '), 'registry bearer challenge required')
        values = urllib.request.parse_keqv_list(urllib.request.parse_http_list(challenge[7:]))
        realm = urllib.parse.urlsplit(values.get('realm', ''))
        require(origin(urllib.parse.urlunsplit((realm.scheme, realm.netloc, '', '', ''))) == origin(self.h['management_url'])
                and realm.path == '/service/token' and not realm.query and not realm.fragment,
                'registry token realm outside approved Harbor')
        credential = decode(private_bytes(str(auth)))['auths'][self.h['registry_authority']]['auth']
        query = urllib.parse.urlencode({'service': values.get('service', ''), 'scope': 'repository:' + repository + ':pull'})
        token_status, _, token_body = self.http(self.h['management_url'], self.h['ca_file'], 'GET',
                                              realm.path + '?' + query, headers={'Authorization': 'Basic ' + credential})
        if token_status in (401, 403):
            return token_status, b''
        require(token_status == 200, 'registry token service failed')
        token = decode(token_body).get('token') or decode(token_body).get('access_token')
        require(isinstance(token, str) and token, 'registry token missing')
        headers['Authorization'] = 'Bearer ' + token
        status, _, body = self.http(self.h['management_url'], self.h['ca_file'], 'GET', endpoint, headers=headers)
        return status, body

    def preflight(self):
        require(sys.platform == 'linux' and 'Fedora' in Path('/etc/os-release').read_text(), 'Fedora execution required')
        group = Path('/sys/fs/cgroup') / Path('/proc/self/cgroup').read_text().strip().split('::', 1)[1].lstrip('/')
        cpu = (group / 'cpu.max').read_text().split()
        require(cpu[0] != 'max' and int(cpu[0]) <= 2 * int(cpu[1])
                and int((group / 'memory.max').read_text()) <= 2300 * 1024 * 1024
                and (group / 'memory.swap.max').read_text().strip() == '0', 'bounded systemd scope required')
        files = [self.profile_path, self.s['runtime_config'], self.s['operator_config'],
                 self.p['cluster']['kubeconfig_secret_file'], self.h['management_credential_file'],
                 *self.p['user_credential_files']]
        for path in files:
            private_bytes(path)
        required_pins = files[1:] + [self.h['ca_file'], self.s['governance_ca_file'],
                                    self.s['resource_binary'], self.s['runtime_binary']]
        for path in required_pins:
            require(self.s['file_sha256'].get(path) == sha(Path(path).read_bytes()), 'pinned input hash mismatch')
        runtime = decode(private_bytes(self.s['runtime_config']))['image']
        operator = decode(private_bytes(self.s['operator_config']))
        require(runtime['enabled'] is True and runtime['harbor_url'].rstrip('/') == origin(self.h['management_url'])
                and runtime['harbor_ca_file'] == self.h['ca_file']
                and runtime['harbor_password_file'] == self.h['management_credential_file']
                and runtime['platform_project'] == self.s['platform_project']
                and runtime['installation_id'] == operator['installation_id']
                and operator['secret_output_directory'] == str(self.private), 'operator/runtime/profile bindings differ')
        for key in ('database_dsn_file', 'encryption_keys_file', 'cursor_signing_key_file'):
            path = runtime[key]
            require(self.s['file_sha256'].get(path) == sha(private_bytes(path)), 'runtime private input hash mismatch')
        self.runtime_config = runtime
        repo = Path(__file__).resolve().parent.parent
        head = self.command('source-sha', ['git', '-C', str(repo), 'rev-parse', 'HEAD']).decode().strip()
        require(head == self.s['resource_sha'], 'probe source SHA differs from profile')
        require(not self.command('source-clean', ['git', '-C', str(repo), 'status', '--porcelain']), 'dirty probe source')
        for binary in (self.s['resource_binary'], self.s['runtime_binary']):
            info = self.command('binary-source', ['go', 'version', '-m', binary]).decode()
            require('vcs.revision=' + head in info and 'vcs.modified=false' in info, 'binary source binding unavailable')
        for tool in ('kubectl', 'skopeo'):
            version = self.command(tool + '-version', [tool, 'version', '--client=true', '-o', 'json'] if tool == 'kubectl' else [tool, '--version'])
            require(sha(version) == self.s['tool_version_sha256'][tool], 'tool version binding mismatch')
        cluster = self.p['cluster']
        args = ['kubectl', '--kubeconfig', cluster['kubeconfig_secret_file'], '--context', cluster['context_name']]
        config = decode(self.command('cluster-config', args + ['config', 'view', '--minify', '--raw', '-o', 'json']))
        bound = config['clusters'][0]['cluster']
        require(bound['server'].rstrip('/') == origin(cluster['api_server'])
                and not bound.get('insecure-skip-tls-verify', False) and not bound.get('proxy-url')
                and sha(base64.b64decode(bound['certificate-authority-data'], validate=True)) == cluster['ca_sha256'],
                'cluster API/CA binding mismatch')
        uid = self.command('cluster-identity', args + ['--request-timeout=20s', 'get', 'namespace', 'kube-system', '-o', 'jsonpath={.metadata.uid}']).decode()
        require(uid == self.s['cluster_uid'], 'cluster UID mismatch')
        status, _, body = self.http(self.h['management_url'], self.h['ca_file'], 'GET', '/api/v2.0/systeminfo')
        require(status == 200 and decode(body)['harbor_version'] == self.h['actual_version'], 'actual Harbor version mismatch')
        password = private_bytes(self.h['management_credential_file']).decode()
        management = base64.b64encode((runtime['harbor_username'] + ':' + password).encode()).decode()
        # All three projects must be absent before the first external write. No
        # existing project is claimed, and no global catalog body is archived.
        for project in self.h['allowed_project_names']:
            status, _, body = self.http(self.h['management_url'], self.h['ca_file'], 'GET',
                '/api/v2.0/projects/' + urllib.parse.quote(project, safe=''),
                headers={'Authorization': 'Basic ' + management})
            require(status == 404, 'project already exists or preflight unavailable; recovery required')
        for i in range(2):
            error = self.gov(i, 'GET', '/space', expected=404)
            require(error.get('reason') == 'SPACE_NOT_FOUND', 'Governance Image space absence is unconfirmed')
        self.base_manifest = self.inspect(self.p['base_image_digest'], self.private / 'anonymous.json', False)
        manifest = decode(self.base_manifest)
        require(sha(self.base_manifest) == self.p['base_image_digest'].rsplit(':', 1)[1], 'base root digest mismatch')
        require(manifest.get('schemaVersion') == 2 and manifest.get('mediaType') in (
            'application/vnd.oci.image.manifest.v1+json', 'application/vnd.docker.distribution.manifest.v2+json')
            and isinstance(manifest.get('layers'), list) and len(manifest['layers']) <= 32,
            'smoke base must be a bounded single-platform manifest')
        sizes = [manifest['config']['size']] + [v['size'] for v in manifest['layers']]
        require(all(type(v) is int and v >= 0 for v in sizes) and sum(sizes) <= self.s['max_image_bytes'], 'base exceeds byte budget')
        self.event('preflight-pass', resource_sha=head, governance_sha=self.s['governance_sha'], cluster_uid=uid,
                   profile_sha256=sha(private_bytes(self.profile_path)), base_digest=self.p['base_image_digest'].split('@')[1])

    def init(self):
        self.stage = 'init'
        space = self.admin('init-platform', {'idempotency_key': self.key('platform-enable')})
        require(space['ProjectName'] == self.s['platform_project'] and space['State'] == 'available', 'platform initialization mismatch')
        self.event('space-created', scope='platform', space_id=space['ID'], project_id=space['ProjectID'],
                   project_name=space['ProjectName'], installation_id=space['InstallationID'])
        self.spaces.append(space)
        delivery_file = self.private / 'platform-delivery.json'
        self.admin('issue-platform-publisher', {'idempotency_key': self.key('platform-issue'), 'expected_version': 0, 'rotate': False}, delivery_file)
        delivery = decode(private_bytes(str(delivery_file)))
        self.auths.append(self.auth_file('platform-publisher', delivery['credential']['Username'], delivery['secret']))
        self.event('publisher-issued', scope='platform', robot_id=delivery['credential']['RobotID'], generation=delivery['credential']['Generation'])
        for i, tenant in enumerate(self.p['test_tenants']):
            self.event('enable-intent', tenant_id=tenant['tenant_id'], idempotency_key=self.key('enable-' + str(i)))
            space = self.gov(i, 'POST', '/space:enable', {'slug': tenant['slug'], 'idempotencyKey': self.key('enable-' + str(i))})['space']
            require(space['projectName'] == tenant['project_name']
                    and space['registryAuthority'] == self.h['registry_authority'] and space['state'] == 'available', 'tenant space binding mismatch')
            facts = self.admin('inspect-space', {'space_id': space['spaceId']})
            require(facts['TenantID'] == tenant['tenant_id'] and facts['ProjectName'] == tenant['project_name']
                    and facts['InstallationID'] == self.runtime_config['installation_id'], 'operator space binding mismatch')
            self.spaces.append(facts)
            self.event('space-created', scope='tenant', tenant_id=tenant['tenant_id'], space_id=facts['ID'],
                       project_id=facts['ProjectID'], project_name=facts['ProjectName'], installation_id=facts['InstallationID'])
            delivery = self.gov(i, 'POST', '/publisher-credential:issue', {'expectedVersion': '0', 'idempotencyKey': self.key('issue-' + str(i))})
            require(delivery['credential']['spaceId'] == facts['ID'], 'publisher space mismatch')
            self.auths.append(self.auth_file('publisher-' + str(i), delivery['credential']['username'], delivery['secret']))
            self.event('publisher-issued', scope='tenant', tenant_id=tenant['tenant_id'], generation=delivery['credential']['generation'])

    def push(self):
        self.stage = 'push'
        self.references = []
        for space, auth in zip(self.spaces, self.auths):
            tag = self.h['registry_authority'] + '/' + space['ProjectName'] + '/smoke:' + self.p['run_id']
            self.event('push-intent', project_id=space['ProjectID'], reference=tag)
            self.command('registry-direct-push', ['skopeo', 'copy', '--src-tls-verify=true', '--dest-tls-verify=true', '--preserve-digests', '--retry-times', '0',
                '--src-authfile', str(self.private / 'anonymous.json'), '--dest-authfile', str(auth),
                '--dest-cert-dir', str(self.private / 'certs'), 'docker://' + self.p['base_image_digest'], 'docker://' + tag], timeout=240)
            actual = self.inspect(tag, auth)
            digest = 'sha256:' + sha(actual)
            require(actual == self.base_manifest, 'pushed root differs from approved base')
            fixed = tag.rsplit(':', 1)[0] + '@' + digest
            self.references.append(fixed)
            self.event('push-pass', project_id=space['ProjectID'], reference=fixed, digest=digest)
        # First establish B exists and its current credential works. Network/TLS,
        # 404, rate-limit and 5xx errors cannot count as tenant isolation success.
        status, body = self.registry_get(self.references[2], self.auths[2])
        require(status == 200 and sha(body) == sha(self.base_manifest), 'B positive registry control failed')
        status, body = self.registry_get(self.references[1], self.auths[1])
        require(status == 200 and sha(body) == sha(self.base_manifest), 'A positive registry control failed')
        status, _ = self.registry_get(self.references[2], self.auths[1])
        require(status in (401, 403), 'A credential can access B or denial is inconclusive')
        self.event('cross-tenant-registry-denied', status=status, from_tenant=self.p['test_tenants'][0]['tenant_id'],
                   to_project_id=self.spaces[2]['ProjectID'])

    def register(self):
        self.stage = 'register'
        platform = self.admin('register-platform', {'idempotency_key': self.key('register-platform'),
            'image_reference': self.references[0], 'display_name': self.p['run_id'] + ' platform',
            'description': 'Run-owned technical probe', 'purposes': ['container'], 'accelerator': 'none'})
        require(platform['ResolvedReference'] == self.references[0], 'platform registration digest mismatch')
        self.images.append(platform['ID'])
        self.event('registered', scope='platform', image_id=platform['ID'], digest=platform['Digest'], version=platform['Version'])
        for i in range(2):
            image = self.gov(i, 'POST', '/registrations', {'idempotencyKey': self.key('register-' + str(i)),
                'imageReference': self.references[i + 1], 'displayName': self.p['run_id'] + ' tenant',
                'description': 'Run-owned technical probe', 'purposes': ['container'],
                'accelerator': 'none'})['image']
            require(image['resolvedReference'] == self.references[i + 1], 'tenant registration digest mismatch')
            self.images.append(image['image_id'])
            self.event('registered', scope='tenant', tenant_id=self.p['test_tenants'][i]['tenant_id'],
                       image_id=image['image_id'], digest=image['digest'], version=image['version'])
        self.gov(0, 'GET', '/registrations/' + self.images[2] + '?scope=tenant', expected=404)

    def resolve(self):
        self.stage = 'resolve'
        for i, tenant in enumerate(self.p['test_tenants']):
            for j in (0, i + 1):
                output = self.private / ('pull-' + str(i) + '-' + str(j) + '.json')
                resolved = decode(self.command('technical-runtime-resolve', [self.s['runtime_binary'], '-profile',
                    self.profile_path, '-auth-output', str(output)], json.dumps({'tenant_id': tenant['tenant_id'],
                    'image_id': self.images[j], 'scope': 'platform' if j == 0 else 'tenant',
                    'platform': self.s['target_platform']}).encode()))
                require(resolved['Reference'] == self.references[j] and resolved['TenantID'] == tenant['tenant_id'], 'runtime immutable result mismatch')
                raw = self.inspect(resolved['Reference'], output)
                require(sha(raw) == resolved['Digest'].split(':')[1], 'runtime pull material cannot read fixed digest')
                status, _ = self.registry_get(self.references[2 - i], output)
                require(status in (401, 403), 'runtime pull material crossed tenant boundary')
                self.event('resolved', tenant_id=tenant['tenant_id'], image_id=self.images[j],
                           reference=resolved['Reference'], digest=resolved['Digest'], cross_tenant_denied=status)

    def run(self):
        self.work.mkdir(mode=0o700)  # Existing run is a recovery case, never blindly repeated.
        self.private.mkdir(mode=0o700)
        new_private(self.events, b'')
        new_private(self.private / 'anonymous.json', b'{"auths":{}}')
        certs = self.private / 'certs'; certs.mkdir(mode=0o700)
        new_private(certs / 'ca.crt', Path(self.h['ca_file']).read_bytes())
        try:
            self.preflight(); self.init(); self.push(); self.register(); self.resolve()
            self.event('technical-pass', product='blocked', cleanup='external resources retained for ID/ownership review')
        finally:
            # Only these locally owned private files are removed. No Harbor or
            # cluster deletion is inferred from a successful or failed probe.
            for path in self.private.iterdir():
                if path.is_file() and not path.is_symlink():
                    path.unlink()
            self.event('cleanup', local_delivery_files='removed', external_resources='retained',
                       recovery='Use event intents and exact IDs; do not rerun with new keys or claim unknown objects')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--profile', required=True, type=Path)
    parser.add_argument('--workdir', required=True, type=Path)
    args = parser.parse_args()
    probe = None
    try:
        profile = validate_profile(decode(private_bytes(str(args.profile))))
        require(args.workdir.is_absolute() and str(args.workdir) == profile['smoke']['work_directory']
                and not args.workdir.exists() and not args.workdir.is_symlink(), 'new approved absolute workdir required')
        def deadline(_signum, _frame):
            raise Stop('probe deadline exceeded')
        signal.signal(signal.SIGALRM, deadline)
        signal.setitimer(signal.ITIMER_REAL, profile['smoke']['timeout_seconds'])
        probe = Probe(profile, args.profile, args.workdir)
        probe.run()
        print('Image technical smoke passed; product acceptance remains unverified. Evidence: ' + str(probe.events))
        return 0
    except (Stop, KeyError, TypeError, ValueError, OSError, subprocess.SubprocessError, urllib.error.URLError) as error:
        # Do not stringify arbitrary dependency exceptions: they can contain URLs,
        # tokens, DSNs or remote response bodies. Stop messages are fixed local text.
        reason = str(error) if isinstance(error, Stop) else 'probe failed; private configuration/dependency check required'
        if probe and probe.events.is_file():
            probe.event('failed', reason=reason)
        print(reason, file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())

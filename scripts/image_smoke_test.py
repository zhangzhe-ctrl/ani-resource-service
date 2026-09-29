"""Offline driver guards and protocol choreography; not Harbor/live evidence."""
import copy
import io
import json
from pathlib import Path
import socket
import tempfile
import unittest
from unittest import mock

import image_smoke as smoke


def profile():
    run = 'smoke-test-01'
    return {
        'format': 'image-mvp-live-profile/v1', 'approved': True, 'approval_reference': 'unit-only', 'run_id': run,
        'cluster': {'api_server': 'https://cluster.invalid', 'context_name': 'unit', 'ca_sha256': 'a' * 64,
                    'allowed_namespaces': ['unit'], 'kubeconfig_secret_file': '/unit/kubeconfig'},
        'harbor': {'management_url': 'https://registry.invalid', 'registry_authority': 'registry.invalid',
                   'allow_existing_platform_write': False, 'actual_version': 'unit', 'ca_file': '/unit/ca',
                   'allowed_project_names': [run + '-platform', 't-' + run + '-a', 't-' + run + '-b']},
        'smoke': {'execution_host': socket.gethostname(), 'allow_technical_runtime_read': True,
                  'cluster_uid': '10000000-0000-4000-8000-000000000001', 'platform_project': run + '-platform',
                  'timeout_seconds': 60, 'max_image_bytes': 100000, 'resource_sha': 'a' * 40,
                  'governance_sha': 'b' * 40, 'runtime_binary': '/unit/runtime',
                  'target_platform': {'OS': 'linux', 'Architecture': 'amd64', 'Variant': ''}},
        'resource_budget': {'go_cpu_quota_percent': 200, 'go_memory_max': '2300M', 'go_swap_max': 0},
        'base_image_digest': 'docker.io/library/unit@sha256:' + 'c' * 64,
        'test_tenants': [{'tenant_id': '20000000-0000-4000-8000-00000000000' + str(i+1),
                          'slug': run + '-' + x, 'project_name': 't-' + run + '-' + x}
                         for i, x in enumerate('ab')],
        'user_credential_files': ['/unit/a.jwt', '/unit/b.jwt'],
        'cleanup': {'allowed': False, 'require_uid_and_owner_match': True, 'global_gc_allowed': False},
    }


class GuardTests(unittest.TestCase):
    def test_profile_rejects_approval_scope_budget_and_source_drift(self):
        smoke.validate_profile(profile())
        changes = [('approved', False), ('approval_reference', ''), ('run_id', '../escape'),
                   ('test_tenants', [profile()['test_tenants'][0]]), ('base_image_digest', 'alpine:latest')]
        for key, value in changes:
            p = profile(); p[key] = value
            with self.subTest(key=key), self.assertRaises(smoke.Stop): smoke.validate_profile(p)
        for section, key, value in [
            ('harbor', 'allow_existing_platform_write', True), ('harbor', 'registry_authority', 'other.invalid'),
            ('harbor', 'allowed_project_names', ['shared']), ('smoke', 'allow_technical_runtime_read', False),
            ('smoke', 'max_image_bytes', 2**30), ('smoke', 'timeout_seconds', 1201),
            ('smoke', 'resource_sha', 'main'), ('smoke', 'execution_host', 'another-host'),
            ('cleanup', 'global_gc_allowed', True), ('resource_budget', 'go_swap_max', 1)]:
            p = profile(); p[section][key] = value
            with self.subTest(key=key), self.assertRaises(smoke.Stop): smoke.validate_profile(p)
        p = profile(); p['test_tenants'][1]['tenant_id'] = p['test_tenants'][0]['tenant_id']
        with self.assertRaises(smoke.Stop): smoke.validate_profile(p)

    def test_private_file_and_json_guards(self):
        with tempfile.TemporaryDirectory() as directory:
            p = Path(directory) / 'input'
            smoke.new_private(p, b'{"ok":true}')
            self.assertEqual(p.stat().st_mode & 0o777, 0o600)
            self.assertEqual(smoke.decode(smoke.private_bytes(str(p))), {'ok': True})
            with self.assertRaises(FileExistsError): smoke.new_private(p, b'replacement')
            link = p.with_name('link'); link.symlink_to(p)
            with self.assertRaises(smoke.Stop): smoke.private_bytes(str(link))
            p.chmod(0o644)
            with self.assertRaises(smoke.Stop): smoke.private_bytes(str(p))
        with self.assertRaises(smoke.Stop): smoke.decode('{"approved":false,"approved":true}')
        for value in ('http://host', 'https://u:p@host', 'https://host/path', 'https://host?q=x'):
            with self.assertRaises(smoke.Stop): smoke.origin(value)

    def test_unapproved_main_never_creates_workdir_or_runs_commands(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'profile'; work = Path(directory) / 'work'
            p = profile(); p['approved'] = False; smoke.new_private(path, json.dumps(p).encode())
            with mock.patch('sys.argv', ['image-smoke', '--profile', str(path), '--workdir', str(work)]), \
                    mock.patch('subprocess.run') as run, mock.patch('sys.stderr', io.StringIO()):
                self.assertEqual(smoke.main(), 1)
            run.assert_not_called(); self.assertFalse(work.exists())


class ProtocolFixture(smoke.Probe):
    """Local shape/control fixture only; no replacement for actual dependencies."""
    def preflight(self):
        self.runtime_config = {'installation_id': 'fixture-installation'}
        self.base_manifest = b'{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}'

    def fixture_space(self, index):
        return {'ProjectName': self.h['allowed_project_names'][index], 'ID': 'space-' + str(index),
                'ProjectID': index + 1, 'State': 'available', 'InstallationID': 'fixture-installation',
                'TenantID': '' if index == 0 else self.p['test_tenants'][index-1]['tenant_id']}

    def admin(self, action, body, secret=None):
        if action == 'init-platform': return self.fixture_space(0)
        if action == 'issue-platform-publisher':
            smoke.new_private(secret, json.dumps({'credential': {'Username': 'platform', 'RobotID': 100,
                            'Generation': 1}, 'secret': 'sensitive-fixture-password'}).encode())
            return {}
        if action == 'inspect-space': return self.fixture_space(int(body['space_id'][-1]))
        if action == 'register-platform':
            return {'ResolvedReference': self.references[0], 'ID': 'image-0', 'Digest': 'sha256:' + smoke.sha(self.base_manifest), 'Version': 1}
        raise AssertionError(action)

    def gov(self, index, method, path, body=None, expected=200):
        if path == '/space:enable':
            self.assert_no_tenant(body)
            space = self.fixture_space(index + 1)
            return {'space': {'projectName': space['ProjectName'], 'spaceId': space['ID'],
                             'registryAuthority': self.h['registry_authority'], 'state': 'available'}}
        if path == '/publisher-credential:issue':
            return {'credential': {'spaceId': 'space-' + str(index + 1), 'username': 'user-' + str(index),
                    'generation': '1'}, 'secret': 'sensitive-fixture-password'}
        if path == '/registrations':
            self.assert_no_tenant(body)
            assert body['purposes'] == ['container'] and body['accelerator'] == 'none'
            return {'image': {'resolvedReference': self.references[index + 1], 'image_id': 'image-' + str(index + 1),
                             'digest': 'sha256:' + smoke.sha(self.base_manifest), 'version': '1'}}
        assert expected == 404 and path.endswith('?scope=tenant')
        return {'reason': 'IMAGE_NOT_FOUND'}

    @staticmethod
    def assert_no_tenant(body):
        assert not any('tenant' in key.lower() for key in body)

    def inspect(self, reference, auth, destination=True):
        return self.base_manifest

    def registry_get(self, reference, auth):
        if 'pull-' in auth.name: return 403, b''
        index = int(auth.stem[-1]) + 1
        return (200, self.base_manifest) if reference == self.references[index] else (403, b'')

    def command(self, label, args, body=None, timeout=60):
        if label == 'registry-direct-push':
            assert '--preserve-digests' in args and '--dest-tls-verify=true' in args
            assert '--src-authfile' in args and '--dest-authfile' in args
            assert 'sensitive-fixture-password' not in str(args)
            return b''
        assert label == 'technical-runtime-resolve'
        request = smoke.decode(body); index = int(request['image_id'][-1])
        smoke.new_private(Path(args[-1]), b'{"auths":{}}')
        return json.dumps({'Reference': self.references[index], 'TenantID': request['tenant_id'],
                           'Digest': 'sha256:' + smoke.sha(self.base_manifest)}).encode()


class ChoreographyTests(unittest.TestCase):
    def test_all_stages_use_public_dto_shapes_and_keep_secret_out_of_events(self):
        with tempfile.TemporaryDirectory() as directory:
            p = profile(); ca = Path(directory) / 'ca'; ca.write_text('fixture-ca'); p['harbor']['ca_file'] = str(ca)
            probe = ProtocolFixture(p, '/unit/profile', Path(directory) / 'work')
            probe.run()
            events = probe.events.read_text()
            self.assertIn('technical-pass', events)
            self.assertIn('cross-tenant-registry-denied', events)
            self.assertEqual(events.count('"kind": "resolved"'), 4)
            self.assertNotIn('sensitive-fixture-password', events)
            self.assertEqual([f for f in probe.private.iterdir() if f.is_file()], [])
            self.assertIn('external_resources', events)

    def test_inconclusive_registry_failure_never_counts_as_isolation(self):
        with tempfile.TemporaryDirectory() as directory:
            p = profile(); ca = Path(directory) / 'ca'; ca.write_text('fixture-ca'); p['harbor']['ca_file'] = str(ca)
            probe = ProtocolFixture(p, '/unit/profile', Path(directory) / 'work')
            with mock.patch.object(probe, 'registry_get', return_value=(503, b'unavailable')):
                with self.assertRaises(smoke.Stop): probe.run()
            self.assertNotIn('technical-pass', probe.events.read_text())
            self.assertEqual([f for f in probe.private.iterdir() if f.is_file()], [])


if __name__ == '__main__':
    unittest.main()

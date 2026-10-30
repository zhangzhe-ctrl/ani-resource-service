#!/usr/bin/env python3
"""Prepare/drive only tenant2's fixed VPC/Subnet through the real Gov APIs.

prepare (default) writes frozen inputs only: no login, GET or business POST.
observe performs authenticated read-only business queries.
execute requires an explicitly assigned execution slot. Unknown admission ACKs
stop the driver; observe first, then explicitly replay the same frozen body/key.
No VM/NAD/namespace/SQL/shared configuration mutation is implemented here.
"""

import argparse
import contextlib
import datetime
import fcntl
import hashlib
import importlib.util
import inspect
import ipaddress
import json
import os
from pathlib import Path
import sys
import time
import urllib.parse
import uuid


RUN = Path('/home/chabking/workspace/ani-resource-service-runs/vm-mvp-20261023-01a168ce')
PRIVATE = RUN / 'private/gov-candidate'
INPUT = PRIVATE / 'tenant2-network-inputs.json'
RECEIPT = RUN / 'evidence/gov-tenant2-network.json'
LOCK = Path('/home/chabking/workspace/ani-network-service-runs/net05a-heavy.lock')
TENANT = 'd87c4dbd-04fa-4066-9ef6-c6fa4217f316'
TENANT_ID = 2
TENANT_CODE = 'vm-isolation-01a168ce'
VPC_CIDR = '10.84.0.0/16'
SUBNET_CIDR = '10.84.1.0/24'
GATEWAY = '10.84.1.1'
NAME = 'vm-mvp-tenant2-01a168ce'
BASE = '/api/v1/networks/'


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def sha(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def fixed_key(kind):
    return str(uuid.uuid5(uuid.NAMESPACE_URL, 'ani://' + TENANT + '/vm-mvp-20261023-01a168ce/' + kind))


def bodies(vpc_id='<original-VPC-ACK-id>'):
    vpc = {'name': NAME, 'cidr': VPC_CIDR, 'description': 'VM MVP tenant2 V10 preparation',
           'idempotency_key': fixed_key('vpc')}
    subnet = {'name': NAME, 'vpc_id': vpc_id, 'cidr': SUBNET_CIDR, 'gateway': GATEWAY,
              'description': 'VM MVP tenant2 V10 preparation', 'idempotency_key': fixed_key('subnet')}
    return {'vpc': vpc, 'subnet': subnet}


def helper():
    connection = os.environ.get('SSH_CONNECTION', '').split()
    require(len(connection) == 4 and connection[2] == '172.16.101.31', 'execute only through ssh fedora')
    source = RUN / 'gov-candidate.py'
    spec = importlib.util.spec_from_file_location('vm_task_gov_candidate', source)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    require(module.RUN == RUN and module.PRIVATE == PRIVATE and module.CLUSTER == '5277649d-e28d-4a0a-ae76-8354365c63bc', 'unexpected Gov helper scope')
    require(module.HTTP == 'http://127.0.0.1:39180' and module.ORIGIN == 'http://127.0.0.1:39188', 'unexpected Gov endpoint/origin')
    return module


def helper_digest(g):
    return sha({name: inspect.getsource(getattr(g, name)) for name in ('api', 'save', 'ProductAPIError')})


def principal():
    record = json.loads((RUN / 'evidence/gov-isolation-product.json').read_text())['principal']
    require(record == {'tenant_id': TENANT_ID, 'resource_tenant_id': TENANT, 'tenant_code': TENANT_CODE}, 'existing tenant2 principal changed')
    path = PRIVATE / 'isolation-credentials.json'
    require(not path.is_symlink() and path.stat().st_mode & 0o077 == 0, 'private tenant2 credential permissions changed')
    credentials = json.loads(path.read_text())
    require(credentials.get('tenant_code') == TENANT_CODE and credentials.get('username') and credentials.get('password'), 'existing tenant2 credentials invalid')
    return record, credentials


def validate_input(state, g):
    require(state['principal'] == {'tenant_id': TENANT_ID, 'resource_tenant_id': TENANT, 'tenant_code': TENANT_CODE}, 'frozen principal changed')
    require(state['helper_functions_sha256'] == helper_digest(g), 'Gov API/save helper changed; review without replacing admission keys')
    require(state['vpc']['body'] == bodies()['vpc'], 'frozen VPC request changed')
    vpc_id = state['vpc'].get('resource_id')
    expected = bodies(vpc_id or '<original-VPC-ACK-id>')['subnet']
    require(state['subnet']['planned_body'] == bodies()['subnet'], 'frozen Subnet plan changed')
    if state['subnet'].get('body') is not None:
        require(vpc_id and state['subnet']['body'] == expected, 'Subnet parent/body changed')
    for kind in ('vpc', 'subnet'):
        body = state[kind].get('body')
        require(body is None or sha(body) == state[kind]['body_sha256'], 'frozen request digest changed')


def prepare(g):
    record, _ = principal()
    require(ipaddress.ip_network(SUBNET_CIDR).subnet_of(ipaddress.ip_network(VPC_CIDR)), 'fixed Subnet outside VPC')
    if INPUT.exists():
        state = json.loads(INPUT.read_text())
        validate_input(state, g)
        return state
    planned = bodies()
    state = {'principal': record, 'prepared_at': now(), 'helper_functions_sha256': helper_digest(g),
             'vpc': {'body': planned['vpc'], 'body_sha256': sha(planned['vpc']), 'send_attempts': 0, 'outcome': 'not_sent'},
             'subnet': {'planned_body': planned['subnet'], 'body': None, 'body_sha256': None, 'send_attempts': 0, 'outcome': 'not_sent'}}
    # Stable keys are persisted before any login or business request. A Subnet's
    # real parent is frozen once, only from this original VPC's acknowledged ID.
    g.save(INPUT, state)
    return state


def public_receipt(g, state, phase, error=None):
    result = {'at': now(), 'scope': 'tenant2 real Gov network; no VM/NAD/namespace/config/SQL mutation',
              'phase': phase, 'principal': state['principal'], 'private_state_reference': str(INPUT),
              'private_credentials_reference': str(PRIVATE / 'isolation-credentials.json'),
              'requests': {}, 'namespace': None, 'namespace_uid': None, 'kcn_subnet_ref': None,
              'provider_identity_status': 'NOT_MEASURED_BY_THIS_DRIVER', 'events': g.EVENTS}
    for kind in ('vpc', 'subnet'):
        result['requests'][kind] = {k: v for k, v in state[kind].items() if k not in ('private',)}
    if error:
        result['error'] = error
    g.save(RECEIPT, result)
    return result


def alias(value, snake, camel):
    return value.get(snake, value.get(camel))


def verify_resource(kind, resource, body):
    require(isinstance(resource, dict) and resource.get('id', '').startswith(kind + '_'), 'invalid acknowledged ' + kind + ' identity')
    for key in ('name', 'cidr'):
        require(resource.get(key) == body[key], 'acknowledged ' + kind + ' intent differs: ' + key)
    tenant = alias(resource, 'tenant_id', 'tenantId')
    require(tenant in (None, TENANT), 'unexpected acknowledged resource tenant')
    if kind == 'subnet':
        require(alias(resource, 'vpc_id', 'vpcId') == body['vpc_id'] and resource.get('gateway') == GATEWAY, 'acknowledged Subnet parent/gateway differs')


def vpcs(g, token):
    result = []
    for page in range(1, 11):
        data = g.api('GET', BASE + 'vpcs?page=' + str(page) + '&pageSize=100', token=token)
        items = data.get('items', [])
        result += items
        require('total' in data, 'VPC pagination total missing')
        if len(result) >= int(data['total']):
            return result
        require(items, 'VPC pagination incomplete')
    raise RuntimeError('tenant2 VPC inventory exceeds bounded preflight')


def subnets(g, token, vpc_id):
    result, cursor, seen = [], '', set()
    for _ in range(10):
        query = urllib.parse.urlencode({'vpc_id': vpc_id, 'limit': 100, **({'cursor': cursor} if cursor else {})})
        data = g.api('GET', BASE + 'subnets?' + query, token=token)
        result += data.get('items', [])
        cursor = alias(data, 'next_cursor', 'nextCursor')
        if not cursor:
            return result
        require(cursor not in seen, 'Subnet cursor repeated')
        seen.add(cursor)
    raise RuntimeError('tenant2 Subnet inventory exceeds bounded preflight')


def inspect_before_post(g, state, kind, token):
    step = state[kind]
    if kind == 'vpc':
        presets = g.api('GET', BASE + 'vpc-cidr-presets', token=token)
        available = presets.get('cidrs')
        require(isinstance(available, list), 'VPC CIDR conflict facts unavailable')
        inventory = vpcs(g, token)
        target = ipaddress.ip_network(VPC_CIDR)
        # Public presets enforce the configured VPC allowlist and persisted pool
        # conflicts. Never edit Resource presets to make this pass.
        facts = {'at': now(), 'offered_cidrs': available, 'inventory_source': 'tenant-scoped public VPC pagination'}
    else:
        inventory = subnets(g, token, state['vpc']['resource_id'])
        target = ipaddress.ip_network(SUBNET_CIDR)
        facts = {'at': now(), 'inventory_source': 'tenant-scoped public parent Subnet cursor pagination'}
    overlaps = [x for x in inventory if ipaddress.ip_network(x['cidr'], strict=True).overlaps(target)]
    names = [x for x in inventory if x.get('name') == NAME]
    candidates = {x['id']: x for x in overlaps + names}
    facts['matching_or_overlapping_resources'] = list(candidates.values())
    step['readonly_preflight'] = facts
    g.save(INPUT, state)
    if kind == 'vpc' and step['send_attempts'] == 0:
        require(VPC_CIDR in available, 'fixed 10.84.0.0/16 is not currently offered; stop without changing CIDR/config')
    own_id = step.get('resource_id')
    for existing in candidates.values():
        if existing['id'] == own_id:
            continue
        if step['outcome'] == 'ack_unknown':
            body = step.get('body') or step.get('planned_body')
            verify_resource(kind, existing, body)
            # Observation is not authority to adopt this ID. Only an explicit
            # same-key replay may recover the original persisted admission ACK.
            continue
        raise RuntimeError('fixed CIDR/name conflicts with unregistered tenant2 resource; no new key/CIDR')


def observe(g, state, token):
    for kind in ('vpc', 'subnet'):
        step = state[kind]
        if step.get('resource_id'):
            resource = g.api('GET', BASE + kind + 's/' + step['resource_id'], token=token)
            verify_resource(kind, resource, step['body'])
            step['current'] = resource
        elif kind == 'vpc' or state['vpc'].get('resource_id'):
            inspect_before_post(g, state, kind, token)
            if step['outcome'] == 'ack_unknown':
                step['unknown_ack_observed_at'] = now()
        g.save(INPUT, state)


def accepted(g, state, kind, token, replay):
    step = state[kind]
    if step.get('resource_id'):
        return
    require(step.get('body') is not None and sha(step['body']) == step['body_sha256'], 'request body not frozen')
    if step['outcome'] != 'not_sent':
        require(step['outcome'] == 'ack_unknown' and replay == kind and step.get('unknown_ack_observed_at'),
                'previous admission not safely repeatable; observe first, then explicitly replay exact original ' + kind + ' body/key')
    inspect_before_post(g, state, kind, token)
    step['send_attempts'] += 1
    step['outcome'] = 'ack_unknown'
    step['last_send_at'] = now()
    g.save(INPUT, state)  # Crash/timeout from now is an unknown original ACK.
    try:
        resource = g.api('POST', BASE + kind + 's', step['body'], token)
    except Exception as error:
        step['last_error'] = {'type': type(error).__name__}
        if isinstance(error, g.ProductAPIError):
            step['last_error'].update(status=error.status, reason=error.reason)
            if error.status in (400, 401, 403, 404, 409, 412, 422):
                step['outcome'] = 'rejected'
        g.save(INPUT, state)
        raise RuntimeError('original ' + kind + ' request did not yield a usable ACK; preserve key and inspect recorded outcome') from None
    step['ack_response'] = resource
    g.save(INPUT, state)
    verify_resource(kind, resource, step['body'])
    step.update(resource_id=resource['id'], operation_id=alias(resource, 'last_operation_id', 'lastOperationId'), outcome='acknowledged')
    g.save(INPUT, state)


def ready(g, state, kind, token, seconds):
    deadline = time.monotonic() + seconds
    while True:
        step = state[kind]
        current = g.api('GET', BASE + kind + 's/' + step['resource_id'], token=token)
        verify_resource(kind, current, step['body'])
        step['current'] = current
        g.save(INPUT, state)
        value = str(current.get('state', '')).upper()
        if value in ('AVAILABLE', 'RESOURCE_STATE_AVAILABLE', 'READY', 'RESOURCE_STATE_READY'):
            require(alias(current, 'observation_stale', 'observationStale') is not True,
                    kind + ' observation stale; do not use for child creation')
            return
        require(value not in ('FAILED', 'RESOURCE_STATE_FAILED', 'DELETING', 'DELETED', 'RESOURCE_STATE_DELETED'),
                kind + ' does not reach available: ' + value + ' reason=' + str(current.get('reason', '')))
        require(time.monotonic() < deadline, kind + ' still pending; observe the same ID, do not recreate')
        time.sleep(.5)


@contextlib.contextmanager
def execution_lock():
    with LOCK.open('a') as stream:
        deadline = time.monotonic() + 5
        while True:
            try:
                fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    print('heavy lock busy; no API requests sent', file=sys.stderr)
                    raise SystemExit(73)
                time.sleep(.2)
        yield


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', nargs='?', choices=('prepare', 'observe', 'execute'), default='prepare')
    parser.add_argument('--execute-authorized', action='store_true', help='only after Root assigns a real execution slot')
    parser.add_argument('--replay-unknown', choices=('vpc', 'subnet'), help='explicit exact-body/key replay after a prior observe')
    parser.add_argument('--wait-seconds', type=int, default=15)
    args = parser.parse_args()
    require(args.action == 'execute' or not args.execute_authorized, 'execution flag applies only to execute')
    require(args.action != 'execute' or args.execute_authorized, 'business POST requires explicit authorized slot')
    require(not args.replay_unknown or args.action == 'execute', 'unknown replay is an explicit execute action')
    require(0 <= args.wait_seconds <= 20, 'bounded ready wait must be 0..20 seconds')
    g = helper()
    with (PRIVATE / 'tenant2-network.lock').open('a') as local_lock:
        fcntl.flock(local_lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = prepare(g)
        if args.action == 'prepare':
            result = public_receipt(g, state, 'PREPARED_NO_API_REQUESTS')
        else:
            with execution_lock():
                _, credentials = principal()
                token = g.api('POST', '/api/v1/auth/password/login', {
                    'tenant_name': credentials['tenant_code'], 'username': credentials['username'],
                    'password': credentials['password']})['access_token']
                try:
                    if args.action == 'observe':
                        observe(g, state, token)
                    else:
                        accepted(g, state, 'vpc', token, args.replay_unknown)
                        ready(g, state, 'vpc', token, args.wait_seconds)
                        if state['subnet']['body'] is None:
                            state['subnet']['body'] = bodies(state['vpc']['resource_id'])['subnet']
                            state['subnet']['body_sha256'] = sha(state['subnet']['body'])
                            g.save(INPUT, state)
                        accepted(g, state, 'subnet', token, args.replay_unknown)
                        ready(g, state, 'subnet', token, args.wait_seconds)
                except Exception as error:
                    public_receipt(g, state, 'STOPPED_SAME_ADMISSION_RETAINED', type(error).__name__ + ': ' + str(error))
                    raise
                result = public_receipt(g, state, 'OBSERVED_NO_BUSINESS_POST' if args.action == 'observe' else 'VPC_SUBNET_AVAILABLE_NOT_GUEST_ACCEPTANCE')
        print(json.dumps(result, indent=2))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        raise SystemExit('STOP: ' + str(error))

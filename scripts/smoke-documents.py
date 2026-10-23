#!/usr/bin/env python3
"""Docs API and rollback checks using generated accounts in disposable containers."""
import hashlib
import importlib.util
import os
import pathlib
import secrets
import sys
import uuid

spec = importlib.util.spec_from_file_location(
    'mobile_smoke', pathlib.Path(__file__).with_name('smoke-mobile-server.py'))
mobile = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mobile)
docker, require, Client = mobile.docker, mobile.require, mobile.Client


def run_backend(image, previous, backend):
    name = 'weazl-documents-' + uuid.uuid4().hex[:12]
    volume = name + '-data'
    password, phrase = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    payload = b'Weazldocs immutable revision and attachment fixture\x00\xff'
    checksum = {'X-Weazl-Content-SHA256': hashlib.sha256(payload).hexdigest()}
    document = 'Weazldocs/_integration-tests/fixture/revision.wzdoc'
    route = '/api/v1/documents/content?path=' + document

    def start(selected):
        docker('run', '--detach', '--pull', 'never', '--name', name,
               '--publish', '127.0.0.1::7272', '--cpus', '2', '--memory', '2g',
               '--env', 'WEAZLCLOUD_DATA=/data', '--env', 'WEAZLCLOUD_DESK_ADDR=:7272',
               '--env', 'WEAZLCLOUD_SHARE_ADDR=:7273', '--env', 'WEAZLCLOUD_DRIVE_ADDR=:7274',
               '--env', 'WEAZLCLOUD_STORAGE_BACKEND=' + backend,
               '--volume', volume + ':/data', selected)
        admin = Client(mobile.fixture_origin(name, 7272), cookies=True)
        admin.wait_ready()
        return admin

    def replace(selected):
        docker('stop', '--time', '90', name)
        require(docker('inspect', '--format', '{{.State.ExitCode}}', name).stdout.strip() == '0',
                'fixture did not stop cleanly')
        docker('rm', name)
        admin = start(selected)
        admin.json('POST', '/api/login', {'username': 'documents-smoke', 'password': password})
        admin.json('POST', '/api/unlock', {'passphrase': phrase})
        return admin

    print(f'DOCUMENTS ({backend}): isolated authentication and persistence checks', flush=True)
    try:
        admin = start(image)
        identity = admin.json('GET', '/.well-known/weazlcloud')['instance_id']
        admin.json('POST', '/api/bootstrap', {'username': 'documents-smoke', 'password': password,
                   'vault_passphrase': phrase, 'confirm': phrase}, expected=(201,))
        admin.json('POST', '/api/unlock', {'passphrase': phrase})
        admin.request('PUT', '/api/library?path=existing.txt', b'preserve existing content')
        scopes = ['documents:read', 'documents:write']
        enrolled = admin.json('POST', '/api/v1/devices', {'name': 'Docs fixture', 'scopes': scopes}, expected=(201,))
        require(set(enrolled['scopes']) == set(scopes), 'document scopes mismatch')
        docs = Client(admin.origin, enrolled['token'])
        read = admin.json('POST', '/api/v1/devices', {'name': 'Docs reader', 'scopes': ['documents:read']}, expected=(201,))
        reader = Client(admin.origin, read['token'])
        Client(admin.origin).request('GET', '/api/v1/documents', expected=(401,))
        docs.request('GET', '/api/v1/documents', expected=(404,))
        docs.json('POST', '/api/v1/documents/folder?path=Weazldocs', expected=(201,))
        docs.request('GET', route, expected=(404,))
        docs.json('PUT', route, payload, headers=checksum)
        received, _ = docs.request('GET', route)
        require(received == payload, 'committed document hash mismatch')
        docs.json('PUT', route, payload, headers=checksum)
        changed = b'different revision'
        docs.request('PUT', route, changed,
                     headers={'X-Weazl-Content-SHA256': hashlib.sha256(changed).hexdigest()}, expected=(409,))
        reader.request('PUT', route, payload, headers=checksum, expected=(403,))
        docs.request('GET', '/api/v1/documents/content?path=existing.txt', expected=(400,))
        docs.request('POST', '/api/uploads', {}, expected=(403,))
        page = docs.json('GET', '/api/v1/documents?path=Weazldocs/_integration-tests/fixture')
        require([row['path'] for row in page['files']] == [document], 'document listing mismatch')
        admin = replace(image)
        require(admin.json('GET', '/.well-known/weazlcloud')['instance_id'] == identity,
                'container replacement changed instance identity')
        docs = Client(admin.origin, enrolled['token'])
        require(docs.request('GET', route)[0] == payload, 'document did not survive container replacement')
        require(admin.request('GET', '/api/library?path=existing.txt')[0] == b'preserve existing content',
                'preexisting file changed')
        admin.json('POST', '/api/v1/devices/' + enrolled['device']['id'] + '/revoke')
        docs.request('GET', route, expected=(401,))
        admin = replace(previous)
        require(admin.request('GET', '/api/library?path=' + document)[0] == payload,
                'previous production image cannot read document after rollback')
        require(admin.request('GET', '/api/library?path=existing.txt')[0] == b'preserve existing content',
                'preexisting file changed after rollback')
        print(f'PASS ({backend}): scoped enrollment, hash read-back, immutable retry, isolation, revocation, container replacement and previous-image rollback', flush=True)
    finally:
        docker('rm', '--force', name, check=False)
        docker('volume', 'rm', volume, check=False)


def main():
    mobile.verify_local_docker()
    image = os.environ['WEAZLCLOUD_IMAGE']
    previous = os.environ['WEAZLCLOUD_PREVIOUS_IMAGE']
    for backend in ('restic', 'shared-experimental'):
        run_backend(image, previous, backend)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        message = str(error) if isinstance(error, mobile.SmokeFailure) else type(error).__name__
        print('FAIL: ' + message, file=sys.stderr)
        sys.exit(1)

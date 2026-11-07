#!/usr/bin/env python3
"""Stage the qualified hosted bundle for GitHub publication without rebuilding."""
import argparse
import hashlib
import json
import pathlib
import re
import shutil
import tarfile
import tempfile
import urllib.request

ORIGIN = 'https://usetrace.si'
# The hosted origin's bot protection answered urllib's default "Python-urllib/3.x"
# agent with HTTP 403 (curl, an empty agent and a descriptive agent were accepted),
# so identify this tooling.
USER_AGENT = 'trace-cli-release/1 (+https://github.com/UseTrace-SI/trace-cli)'
LIMIT = 20 * 1024 * 1024
REPOS = {'trace-cli', 'trace-brain', 'trace-graph', 'trace-platform'}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def prepare(version, output, origin=ORIGIN):
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?', version):
        raise ValueError('invalid release version')
    output = pathlib.Path(output)
    if output.exists():
        raise ValueError('output already exists; refusing to overwrite artifacts')
    opener = urllib.request.build_opener(NoRedirect)
    opener.addheaders = [('User-Agent', USER_AGENT)]

    def fetch(path, limit):
        with opener.open(origin + path, timeout=90) as response:
            if response.status != 200:
                raise ValueError('unexpected response status')
            value = response.read(limit + 1)
            if len(value) > limit:
                raise ValueError('download exceeds size budget')
            return value

    raw_stack = fetch('/stack.json', 65536)
    stack = json.loads(raw_stack)
    if stack.get('version') != version or stack.get('sync_protocol') != 2 or stack.get('companion_protocol') != 1:
        raise ValueError('hosted version or protocol mismatch')
    if set(stack.get('sources', {})) != REPOS:
        raise ValueError('incomplete source manifest')
    if not re.fullmatch(r'go[0-9]+\.[0-9]+\.[0-9]+', stack.get('go_toolchain', '')):
        raise ValueError('invalid compiler provenance')
    for name, source in stack['sources'].items():
        if source.get('dirty') is not False or not re.fullmatch(r'[a-f0-9]{40}', source.get('commit', '')):
            raise ValueError('unqualified source manifest')
        if name != 'trace-platform' and source.get('toolchain') != stack.get('go_toolchain'):
            raise ValueError('compiler provenance mismatch')
    names = {f'trace_{version}_{os}_{arch}.tar.gz' for os in ('darwin', 'linux') for arch in ('arm64', 'amd64')}
    artifacts = stack.get('artifacts', [])
    if len(artifacts) != 4 or {a.get('name') for a in artifacts} != names:
        raise ValueError('incomplete target set')
    prefix = '/downloads/v' + version + '/'
    raw_checksums = fetch(prefix + 'checksums.txt', 8192)
    checksums = {}
    for line in raw_checksums.decode().splitlines():
        fields = line.split()
        if len(fields) != 2 or not re.fullmatch(r'[a-f0-9]{64}', fields[0]) or fields[1] not in names or fields[1] in checksums:
            raise ValueError('invalid or ambiguous checksum inventory')
        checksums[fields[1]] = fields[0]
    if set(checksums) != names:
        raise ValueError('missing release checksum')
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = pathlib.Path(tempfile.mkdtemp(prefix='.coordinated-', dir=output.parent))
    try:
        for artifact in artifacts:
            name, size = artifact['name'], artifact.get('bytes')
            if type(size) is not int or not 0 < size <= LIMIT:
                raise ValueError('invalid archive size')
            value = fetch(prefix + name, size)
            if len(value) != size or hashlib.sha256(value).hexdigest() != checksums[name]:
                raise ValueError('archive integrity mismatch')
            path = temporary / name
            path.write_bytes(value)
            with tarfile.open(path, 'r:gz') as archive:
                members = []
                for member in archive:
                    if len(members) == 4 or not member.isfile() or not 0 <= member.size <= 64 * 1024 * 1024 or member.name not in {'trace', 'trace-brain', 'trace-graph', 'stack.json'}:
                        raise ValueError('unsafe or incomplete coordinated archive')
                    members.append(member)
                    if sum(m.size for m in members) > 128 * 1024 * 1024:
                        raise ValueError('oversized expanded archive')
                if len(members) != 4 or {m.name for m in members} != {'trace', 'trace-brain', 'trace-graph', 'stack.json'}:
                    raise ValueError('unsafe or incomplete coordinated archive')
                if archive.getmember('stack.json').size > 65536:
                    raise ValueError('oversized embedded manifest')
                embedded = json.load(archive.extractfile('stack.json'))
                if any(embedded.get(key) != stack.get(key) for key in ('version', 'sources', 'sync_protocol', 'companion_protocol', 'go_toolchain')):
                    raise ValueError('embedded source manifest mismatch')
        (temporary / 'stack.json').write_bytes(raw_stack)
        (temporary / 'checksums.txt').write_bytes(raw_checksums)
        (temporary / 'source-cli.txt').write_text(stack['sources']['trace-cli']['commit'] + '\n')
        temporary.rename(output)
    finally:
        if temporary.exists():
            shutil.rmtree(temporary)
    return stack


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    prepare(args.version.removeprefix('v'), args.out)
    print('Verified and staged all four coordinated release archives.')

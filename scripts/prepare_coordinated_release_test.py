import hashlib
import io
import json
import pathlib
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit
from prepare_coordinated_release import prepare


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.version = '1.1.0-solo.4'
        self.stack = {'version': self.version, 'sync_protocol': 2, 'companion_protocol': 1, 'go_toolchain': 'go1.27.3', 'sources': {name: {'commit': 'a' * 40, 'dirty': False, **({'toolchain': 'go1.27.3'} if name != 'trace-platform' else {})} for name in ('trace-cli', 'trace-brain', 'trace-graph', 'trace-platform')}, 'artifacts': []}
        self.contents = {}
        sums = []
        for os in ('darwin', 'linux'):
            for arch in ('arm64', 'amd64'):
                name = f'trace_{self.version}_{os}_{arch}.tar.gz'
                buffer = io.BytesIO()
                with tarfile.open(fileobj=buffer, mode='w:gz') as archive:
                    for file in ('trace', 'trace-brain', 'trace-graph', 'stack.json'):
                        value = json.dumps(self.stack).encode() if file == 'stack.json' else b'fixture executable'
                        member = tarfile.TarInfo(file);member.size = len(value)
                        archive.addfile(member, io.BytesIO(value))
                value = buffer.getvalue()
                self.contents['/downloads/v' + self.version + '/' + name] = value
                self.stack['artifacts'].append({'name': name, 'bytes': len(value)})
                sums.append(hashlib.sha256(value).hexdigest() + '  ' + name)
        self.contents['/stack.json'] = json.dumps(self.stack).encode()
        self.sum_path = '/downloads/v' + self.version + '/checksums.txt'
        self.contents[self.sum_path] = ('\n'.join(sums) + '\n').encode()
        contents = self.contents
        class Response(io.BytesIO):
            status = 200
        class Opener:
            def open(self, url, timeout):
                return Response(contents[urlsplit(url).path])
        self.transport = patch('prepare_coordinated_release.urllib.request.build_opener', return_value=Opener())
        self.transport.start()
        self.origin = 'https://fixture.invalid'

    def tearDown(self):
        self.transport.stop();self.temp.cleanup()

    def test_complete_bundle_is_preserved_without_rebuilding(self):
        out = self.root / 'release';prepare(self.version, out, self.origin)
        for item in self.stack['artifacts']:
            self.assertEqual((out / item['name']).read_bytes(), self.contents['/downloads/v' + self.version + '/' + item['name']])
        with self.assertRaisesRegex(ValueError, 'overwrite'):prepare(self.version, out, self.origin)

    def test_corrupt_archive_is_not_published(self):
        path = '/downloads/v' + self.version + '/' + self.stack['artifacts'][0]['name'];self.contents[path] = b'corrupt'
        with self.assertRaisesRegex(ValueError, 'integrity'):prepare(self.version, self.root / 'release', self.origin)
        self.assertFalse((self.root / 'release').exists())
        self.assertEqual(list(self.root.iterdir()), [])

    def test_duplicate_checksum_and_dirty_sources_are_refused(self):
        self.contents[self.sum_path] += self.contents[self.sum_path].splitlines()[0] + b'\n'
        with self.assertRaisesRegex(ValueError, 'ambiguous'):prepare(self.version, self.root / 'release', self.origin)
        self.stack['sources']['trace-cli']['dirty'] = True;self.contents['/stack.json'] = json.dumps(self.stack).encode()
        with self.assertRaisesRegex(ValueError, 'unqualified'):prepare(self.version, self.root / 'release', self.origin)

    def test_version_and_target_mismatches_are_refused(self):
        with self.assertRaisesRegex(ValueError, 'version'):prepare('2.0.0', self.root / 'release', self.origin)
        self.stack['artifacts'][0]['name'] = '../outside';self.contents['/stack.json'] = json.dumps(self.stack).encode()
        with self.assertRaisesRegex(ValueError, 'target'):prepare(self.version, self.root / 'release', self.origin)

    def test_valid_checksum_cannot_hide_incomplete_archive(self):
        item = self.stack['artifacts'][0];buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode='w:gz') as archive:
            member = tarfile.TarInfo('trace');member.size = 1;archive.addfile(member, io.BytesIO(b'x'))
        value = buffer.getvalue();item['bytes'] = len(value)
        self.contents['/downloads/v' + self.version + '/' + item['name']] = value
        sums = self.contents[self.sum_path].decode().splitlines();sums[0] = hashlib.sha256(value).hexdigest() + '  ' + item['name'];self.contents[self.sum_path] = ('\n'.join(sums) + '\n').encode()
        self.contents['/stack.json'] = json.dumps(self.stack).encode()
        with self.assertRaisesRegex(ValueError, 'incomplete coordinated'):prepare(self.version, self.root / 'release', self.origin)
        self.assertFalse((self.root / 'release').exists())


if __name__ == '__main__':unittest.main()

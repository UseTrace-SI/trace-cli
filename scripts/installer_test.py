import hashlib
import json
import os
import pathlib
import platform
import subprocess
import tarfile
import tempfile
import unittest

INSTALLER = pathlib.Path(__file__).resolve().parent / 'install.sh'


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.transport = self.root / 'transport';self.transport.mkdir()
        self.host = 'darwin' if platform.system() == 'Darwin' else 'linux'
        self.arch = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
        self.name = f'trace_1.1.0-solo.4_{self.host}_{self.arch}.tar.gz'
        self.destination = self.root / 'bin with spaces'
        self.env = dict(os.environ, PATH=str(self.transport) + os.pathsep + os.environ['PATH'], TRACE_INSTALL_DIR=str(self.destination), TRACE_FIXTURE=str(self.root))
        for key in ('TRACE_VERSION', 'TRACE_RELEASE_BASE_URL', 'TRACE_REQUIRE_COMPANIONS'):
            self.env.pop(key, None)
        (self.root / 'stack.json').write_text(json.dumps({'version': '1.1.0-solo.4'}, indent=2))
        curl = self.transport / 'curl'
        curl.write_text('''#!/usr/bin/env python3
import os,pathlib,shutil,sys,urllib.parse
args=sys.argv[1:];url=urllib.parse.urlsplit(args[-1]);root=pathlib.Path(os.environ['TRACE_FIXTURE'])
assert args[args.index('--proto')+1]=='=https' and args[args.index('--proto-redir')+1]=='=https'
assert url.scheme=='https' and url.netloc=='usetrace.si'
file=root/pathlib.PurePosixPath(url.path).name
if '-o' in args:shutil.copyfile(file,args[args.index('-o')+1])
else:sys.stdout.buffer.write(file.read_bytes())
''')
        curl.chmod(0o700)

    def tearDown(self):
        self.temp.cleanup()

    def archive(self, companions=True):
        files = ['trace', 'trace-brain', 'trace-graph'] if companions else ['trace']
        with tarfile.open(self.root / self.name, 'w:gz') as archive:
            for name in files:
                file = self.root / name;file.write_text('#!/bin/sh\necho fixture-1.1.0-solo.4\n');file.chmod(0o755)
                archive.add(file, arcname=name)
        digest = hashlib.sha256((self.root / self.name).read_bytes()).hexdigest()
        (self.root / 'checksums.txt').write_text(digest + '  ' + self.name + '\n')

    def run_installer(self):
        return subprocess.run(['/bin/sh', str(INSTALLER)], env=self.env, capture_output=True, text=True)

    def test_default_manifest_installs_full_bundle_and_alias(self):
        self.archive();result = self.run_installer();self.assertEqual(result.returncode, 0, result.stderr)
        for name in ('trace', 'trace-brain', 'trace-graph'):
            self.assertEqual((self.destination / name).read_bytes(), (self.root / name).read_bytes())
            self.assertTrue(os.access(self.destination / name, os.X_OK))
        self.assertEqual((self.destination / 'trc').resolve(), (self.destination / 'trace').resolve())

    def test_bad_checksum_preserves_existing_bundle(self):
        self.archive();self.destination.mkdir()
        for name in ('trace', 'trace-brain', 'trace-graph'):(self.destination / name).write_text('existing')
        (self.root / 'checksums.txt').write_text('a' * 64 + '  ' + self.name + '\n')
        self.assertNotEqual(self.run_installer().returncode, 0)
        for name in ('trace', 'trace-brain', 'trace-graph'):self.assertEqual((self.destination / name).read_text(), 'existing')

    def test_missing_companions_fail_before_install_and_legacy_mode_is_explicit(self):
        self.archive(companions=False)
        self.assertNotEqual(self.run_installer().returncode, 0)
        self.assertFalse((self.destination / 'trace').exists())
        self.env['TRACE_REQUIRE_COMPANIONS'] = '0'
        result = self.run_installer();self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == '__main__':unittest.main()

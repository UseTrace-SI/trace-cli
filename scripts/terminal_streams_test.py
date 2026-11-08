import errno
import os
import pty
import subprocess
import sys
import unittest
from unittest.mock import patch

from terminal_streams import read_terminal_process


class TerminalProcessTests(unittest.TestCase):
    def collect(self, proc, master, timeout):
        read = os.read

        def linux_terminal_eof(fd, size):
            chunk = read(fd, size)
            if not chunk:
                raise OSError(errno.EIO, 'terminal slave closed')
            return chunk

        # Linux reports PTY EOF as EIO; exercise that case on macOS too.
        with patch('terminal_streams.os.read', side_effect=linux_terminal_eof):
            return read_terminal_process(proc, master, timeout)

    def start(self, script):
        try:
            master, slave = pty.openpty()
        except OSError as error:
            self.skipTest(str(error))
        proc = subprocess.Popen([sys.executable, '-c', script], stdout=slave, stderr=slave)
        os.close(slave)
        self.addCleanup(os.close, master)
        self.addCleanup(lambda: (proc.kill(), proc.wait()) if proc.poll() is None else None)
        return proc, master

    def test_terminal_closure_before_process_exit_is_not_a_timeout(self):
        proc, master = self.start("import os,time; print('terminal output',flush=True); os.close(1); os.close(2); time.sleep(0.1)")
        output = self.collect(proc, master, timeout=3)
        self.assertEqual(proc.returncode, 0)
        self.assertIn(b'terminal output', output)

    def test_deadline_kills_and_reaps_a_stalled_process(self):
        proc, master = self.start('import time; time.sleep(10)')
        with self.assertRaisesRegex(TimeoutError, 'terminal run timed out'):
            read_terminal_process(proc, master, timeout=0.05)
        self.assertIsNotNone(proc.poll())

    def test_nonzero_exit_status_is_preserved_after_terminal_closure(self):
        proc, master = self.start("import os,time; print('agent error',flush=True); os.close(1); os.close(2); time.sleep(0.1); raise SystemExit(7)")
        output = self.collect(proc, master, timeout=3)
        self.assertEqual(proc.returncode, 7)
        self.assertIn(b'agent error', output)


if __name__ == '__main__':
    unittest.main()

"""Collect terminal output while enforcing the child process deadline."""

import errno
import os
import select
import subprocess
import time


def read_terminal_process(proc, master, timeout=15):
    data = bytearray()
    deadline = time.monotonic() + timeout
    while proc.poll() is None and time.monotonic() < deadline:
        if select.select([master], [], [], min(0.1, max(0, deadline - time.monotonic())))[0]:
            try:
                chunk = os.read(master, 4096)
                if not chunk:
                    break
                data.extend(chunk)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                break
    # PTY EOF can precede waitpid readiness. Reap within the original deadline
    # rather than treating terminal closure as a stalled child.
    try:
        proc.wait(timeout=max(0, deadline - time.monotonic()))
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
        raise TimeoutError('terminal run timed out') from None
    while select.select([master], [], [], 0)[0]:
        try:
            chunk = os.read(master, 4096)
            if not chunk:
                break
            data.extend(chunk)
        except OSError as error:
            if error.errno != errno.EIO:
                raise
            break
    return bytes(data)

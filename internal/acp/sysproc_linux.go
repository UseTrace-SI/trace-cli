package acp

import "syscall"

// agentSysProcAttr puts the agent in its own process group, so a terminal's
// SIGINT reaches it once (forwarded by Trace) rather than twice, and asks the
// kernel to kill it if Trace dies without stopping it.
func agentSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

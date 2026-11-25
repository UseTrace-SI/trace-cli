package acp

import "syscall"

// agentSysProcAttr asks the kernel to kill the agent if Trace dies without
// stopping it and, when group is set, puts the agent in its own process
// group so signals reach it and its children exactly once.
func agentSysProcAttr(group bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: group, Pdeathsig: syscall.SIGKILL}
}

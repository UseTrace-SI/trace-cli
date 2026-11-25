//go:build !linux

package acp

import "syscall"

// agentSysProcAttr puts the agent in its own process group when group is
// set, so signals reach it and its children exactly once.
func agentSysProcAttr(group bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: group}
}

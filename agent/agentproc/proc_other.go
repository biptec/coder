//go:build !windows

package agentproc

import (
	"errors"
	"os"
	"syscall"
)

// procSysProcAttr returns the SysProcAttr to use when spawning
// processes. On Unix, Setpgid creates a new process group so
// that signals can be delivered to the entire group (the shell
// and all its children).
func procSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// signalProcess sends a signal to the process group rooted at p.
// Using the negative PID sends the signal to every process in the
// group, ensuring child processes (e.g. from shell pipelines) are
// also signaled.
func signalProcess(p *os.Process, sig syscall.Signal) error {
	return syscall.Kill(-p.Pid, sig)
}

func killProcessGroup(p *os.Process) error {
	err := signalProcess(p, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

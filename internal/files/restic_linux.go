//go:build linux

package files

import (
	"os/exec"
	"runtime"
	"syscall"
)

// startProcess starts cmd with PR_SET_PDEATHSIG = SIGKILL: when the service dies (kill -9, OOM),
// the kernel kills restic as well, so a backup whose stdin breaks does not go on to commit the
// stream it has. The kernel sends the signal when the *thread* that forked the child exits, not
// only the process; the Go runtime may retire threads. The fork therefore runs on a goroutine
// that is locked to its thread and stays alive (locked) until the child has been reaped
// (done), so that thread cannot go away before restic does.
func startProcess(cmd *exec.Cmd, done <-chan struct{}) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := cmd.Start()
		started <- err
		if err == nil {
			<-done
		}
	}()
	return <-started
}

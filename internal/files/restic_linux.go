//go:build linux

package files

import (
	"fmt"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// Low priority of a restic child (dev instances spec §4.3): nice 10 and the idle I/O class.
const (
	lowNice          = 10
	ioprioWhoProcess = 1 // IOPRIO_WHO_PROCESS (on a thread id: that thread)
	ioprioClassIdle  = 3 // IOPRIO_CLASS_IDLE
	ioprioClassShift = 13
)

// startProcess starts cmd with PR_SET_PDEATHSIG = SIGKILL: when the service dies (kill -9, OOM),
// the kernel kills restic as well, so a backup whose stdin breaks does not go on to commit the
// stream it has. The kernel sends the signal when the *thread* that forked the child exits, not
// only the process; the Go runtime may retire threads. The fork therefore runs on a goroutine
// that is locked to its thread and stays alive (locked) until the child has been reaped
// (done), so that thread cannot go away before restic does.
//
// low lowers the nice value and the I/O class of that thread before the fork, so the child
// inherits both from its first instruction (setting them on the child's pid afterwards would
// miss the threads restic starts meanwhile). An unprivileged thread cannot raise them again:
// the goroutine then ends without unlocking, which ends the thread as well (runtime.LockOSThread).
// Failing to lower them is not fatal; the error is returned in prioErr.
func startProcess(cmd *exec.Cmd, done <-chan struct{}, low bool) (prioErr, err error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	type result struct{ prio, start error }
	started := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		var prio error
		if low {
			prio = lowerThreadPriority()
		} else {
			defer runtime.UnlockOSThread()
		}
		err := cmd.Start()
		started <- result{prio, err}
		if err == nil {
			<-done
		}
	}()
	r := <-started
	return r.prio, r.start
}

// lowerThreadPriority sets nice lowNice and the idle I/O class on the calling thread.
func lowerThreadPriority() error {
	tid := unix.Gettid()
	if err := unix.Setpriority(unix.PRIO_PROCESS, tid, lowNice); err != nil {
		return fmt.Errorf("set nice %d: %w", lowNice, err)
	}
	if _, _, e := unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(tid), ioprioClassIdle<<ioprioClassShift); e != 0 { //nolint:gosec // G103/G115: tid is a positive thread id, the value a constant
		return fmt.Errorf("set the idle I/O class: %w", e)
	}
	return nil
}

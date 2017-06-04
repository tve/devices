package thread

import (
	"runtime"
	"syscall"
	"unsafe"
)

// Realtime locks the calling goroutine to its own kernel thread and elevates that
// thread's priority to realtime. It sets the round-robin schduling policy and uses
// priority level 10 (somewhere in the lower middle of the range).
func Realtime() error {
	// First pin goroutine to its own kernel thread.
	runtime.LockOSThread()
	// Get the ID of the thread.
	tid := syscall.Gettid()
	// Give this thread realtime priority.
	res, _, err := syscall.RawSyscall(syscall.SYS_SCHED_SETSCHEDULER, uintptr(tid),
		uintptr(RR), uintptr(unsafe.Pointer(&schedParam{10})))
	if res == 0 {
		return nil
	}
	return err
}

const FIFO = 1 // fifo scheduling policy
const RR = 2   // round-robin scheduling policy

type schedParam struct {
	Priority int
}

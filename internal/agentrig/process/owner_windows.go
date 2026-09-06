package process

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ownership struct {
	mu        sync.Mutex
	job, root windows.Handle
	finished  bool
}

func prepare(cmd *exec.Cmd) (*ownership, error) {
	if cmd.SysProcAttr != nil || cmd.Cancel != nil {
		return nil, errors.New("command already has process ownership")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// No child code runs before association. Failure to assign/resume is fatal.
	// An abrupt parent death in this suspended startup window remains a lifecycle gate.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	return &ownership{job: job}, nil
}
func (o *ownership) started(cmd *exec.Cmd) error {
	pid := uint32(cmd.Process.Pid)
	root, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return err
	}
	o.root = root
	if err = windows.AssignProcessToJobObject(o.job, root); err != nil {
		return err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		previous, resumeErr := windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if resumeErr != nil {
			return resumeErr
		}
		if previous != 1 {
			return errors.New("unexpected command suspend count")
		}
		return nil
	}
	return errors.New("suspended command thread unavailable")
}
func (o *ownership) wait() error {
	_, err := windows.WaitForSingleObject(o.root, windows.INFINITE)
	return err
}
func (o *ownership) stop() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.finished {
		return nil
	}
	return windows.TerminateJobObject(o.job, 1)
}
func (o *ownership) finish() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := windows.TerminateJobObject(o.job, 1); err != nil {
		return err
	}
	// TerminateJobObject is asynchronous. Keep the job handle and caller's lease
	// until the kernel reports no active processes, including descendants.
	for {
		var accounting struct {
			User, Kernel, PeriodUser, PeriodKernel int64
			Faults, Total, Active, Terminated      uint32
		}
		if err := windows.QueryInformationJobObject(o.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return err
		}
		if accounting.Active == 0 {
			o.finished = true
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func (o *ownership) close() {
	if o.root != 0 {
		windows.CloseHandle(o.root)
	}
	if o.job != 0 {
		windows.CloseHandle(o.job)
	}
}

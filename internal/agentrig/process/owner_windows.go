package process

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ownership struct {
	mu                sync.Mutex
	job, root, anchor windows.Handle
	finished          bool
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
	// Go exposes ParentProcess but not JOB_LIST. Create a never-resumed anchor
	// atomically in the job, then let exec.Cmd inherit its job at creation.
	// No process exists outside the job, including before started runs.
	anchor, err := createJobAnchor(job)
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{ParentProcess: syscall.Handle(anchor)}
	return &ownership{job: job, anchor: anchor}, nil
}

// createJobAnchor maps this executable but never runs its initial thread. It
// inherits no handles, especially not the kill-on-close job handle. Windows 10
// JOB_LIST association happens inside CreateProcess, closing the suspended-
// process/AssignProcessToJobObject gap. Failure has no unfenced fallback.
func createJobAnchor(job windows.Handle) (windows.Handle, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, err
	}
	path, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return 0, err
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return 0, err
	}
	defer attributes.Delete()
	// PROC_THREAD_ATTRIBUTE_JOB_LIST (Windows SDK); x/sys does not name it.
	const jobList = 0x0002000d
	if err := attributes.Update(jobList, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return 0, err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.ProcThreadAttributeList = attributes.List()
	var info windows.ProcessInformation
	err = windows.CreateProcess(path, nil, nil, nil, false,
		windows.CREATE_SUSPENDED|windows.EXTENDED_STARTUPINFO_PRESENT,
		nil, nil, &startup.StartupInfo, &info)
	runtime.KeepAlive(job)
	if err != nil {
		return 0, err
	}
	windows.CloseHandle(info.Thread)
	return info.Process, nil
}

func (o *ownership) started(cmd *exec.Cmd) error {
	root, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	o.root = root
	return nil
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
	if o.finished {
		return nil
	}
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
			// Job accounting can reach zero before process handles signal. Wait
			// for our direct handles too, including a never-resumed anchor.
			for _, handle := range []windows.Handle{o.anchor, o.root} {
				if handle == 0 {
					continue
				}
				state, err := windows.WaitForSingleObject(handle, windows.INFINITE)
				if err != nil {
					return err
				}
				if state != windows.WAIT_OBJECT_0 {
					return errors.New("command process exit not confirmed")
				}
			}
			o.finished = true
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func (o *ownership) close() error {
	// Also retire the suspended anchor when exec.Cmd.Start fails.
	err := o.finish()
	if o.anchor != 0 {
		err = errors.Join(err, windows.CloseHandle(o.anchor))
	}
	if o.root != 0 {
		err = errors.Join(err, windows.CloseHandle(o.root))
	}
	if o.job != 0 {
		err = errors.Join(err, windows.CloseHandle(o.job))
	}
	return err
}

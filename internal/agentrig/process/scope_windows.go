package process

import (
	"errors"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func currentScope() (recoveryScope, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return recoveryScope{}, err
	}
	defer key.Close()
	host, _, err := key.GetStringValue("MachineGuid")
	if err != nil {
		return recoveryScope{}, err
	}
	host, err = normalizeScopeID(host)
	if err != nil {
		return recoveryScope{}, err
	}
	created, err := systemProcessCreation()
	if err != nil {
		return recoveryScope{}, err
	}
	return recoveryScope{Host: digest(host), Boot: digest("windows-system-process:" + strconv.FormatInt(created, 10)), Namespace: digest("windows-host-processes")}, nil
}

// The kernel's System process (PID 4) lives for the kernel incarnation. Its
// immutable process creation identity survives sleep/hibernation and clock
// changes. Do not substitute wall-clock-minus-uptime, a boot-entry GUID, a logon
// session, or a counter that may also advance during resume.
func systemProcessCreation() (int64, error) {
	// Bound allocation and retries even while unrelated processes are starting.
	for size := uint32(64 << 10); size <= 32<<20; size *= 2 {
		data := make([]byte, size)
		var used uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&data[0]), size, &used)
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if used == 0 || used > size {
			return 0, ErrRecoveryScope
		}
		return systemCreationFromSnapshot(data[:used])
	}
	return 0, ErrRecoveryScope
}

func systemCreationFromSnapshot(data []byte) (int64, error) {
	const header = int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))
	for offset := 0; ; {
		if len(data)-offset < header {
			return 0, ErrRecoveryScope
		}
		info := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&data[offset]))
		next := uint64(info.NextEntryOffset)
		if next != 0 && (next < uint64(header) || next > uint64(len(data)-offset-header) || next%uint64(unsafe.Alignof(windows.SYSTEM_PROCESS_INFORMATION{})) != 0) {
			return 0, ErrRecoveryScope
		}
		if info.UniqueProcessID == 4 {
			if info.InheritedFromUniqueProcessID != 0 || info.SessionID != 0 || info.CreateTime <= 0 {
				return 0, ErrRecoveryScope
			}
			return info.CreateTime, nil
		}
		if next == 0 {
			return 0, ErrRecoveryScope
		}
		offset += int(next)
	}
}

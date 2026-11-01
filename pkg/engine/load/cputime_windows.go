//go:build windows

package load

import (
	"time"

	"golang.org/x/sys/windows"
)

// processCPU is the CPU time (user + kernel) this process has used.
func processCPU() (time.Duration, bool) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		return 0, false
	}
	ticks := func(f windows.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(kernel) + ticks(user)) * 100), true // 100 ns units
}

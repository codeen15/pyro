//go:build !unix && !windows

package load

import "time"

func processCPU() (time.Duration, bool) { return 0, false }

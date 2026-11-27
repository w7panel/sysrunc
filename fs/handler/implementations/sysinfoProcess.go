package implementations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ShouldVirtualizeSysinfoForPID is called by the registered container's proc
// worker. Only the free command needs the synthetic cgroup memory view; other
// callers keep the kernel's sysinfo behavior.
func ShouldVirtualizeSysinfoForPID(pid uint32) bool {
	if pid == 0 {
		return false
	}
	exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if filepath.Base(exe) == "free" {
		return true
	}
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	return err == nil && strings.TrimSpace(string(comm)) == "free"
}

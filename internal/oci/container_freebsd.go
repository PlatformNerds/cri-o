//go:build freebsd && cgo

package oci

// #include <sys/types.h>
// #include <sys/user.h>
import "C"

import (
	"fmt"
	"strings"
	"unsafe"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	types "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const sysctlName = "kern.proc.pid"

// bootRelativePrefix marks a start time measured from boot (see
// getPidStatDataFromSysctl); saved start times without it are wall-clock
// times recorded by older CRI-O versions.
const bootRelativePrefix = "boot:"

// getPidStartTime returns the process start time for a given PID.
func getPidStartTime(pid int) (string, error) {
	return getPidStatDataFromSysctl(pid)
}

// startTimesMatch reports whether a process start time saved for a
// container's PID matches the PID's current one (no PID reuse).
func startTimesMatch(saved, current string, pid int) bool {
	if saved == current {
		return true
	}
	if strings.HasPrefix(saved, bootRelativePrefix) {
		return false
	}
	// Saved by an older CRI-O as wall-clock time: compare like it did.
	abs, _, err := kinfoStartTime(pid)
	return err == nil && saved == fmt.Sprintf("%d,%d", abs.Sec, abs.Usec)
}

// getPidStatData returns the process state and start time for a given PID.
//
// TODO: Return the process state using struct kinfo_proc.
func getPidStatData(pid int) (string, string, error) { //nolint:gocritic // Ignore unnamedResult.
	startTime, err := getPidStartTime(pid)
	return "", startTime, err
}

// getPidStatDataFromSysctl reads the process start time using sysctl. This
// is marginally more efficient than /proc but more importantly, it works
// when /proc is not mounted, which not a requirement on FreeBSD.
//
// TODO: Add process state collection using struct kinfo_proc.
//
// The start time is returned relative to boot. The kernel reports ki_start as
// wall-clock time (boot time + the start's offset from boot), and stepping
// the clock (ntpd -g at boot, ntpdate, settimeofday) moves the boot time and
// with it every process's ki_start: CRI-O then took the container's PID for
// a reused one ("PID wrap may have occurred") and refused exec and other
// operations on it. Linux uses the start time in ticks since boot, which
// clock steps don't change; ki_start - kern.boottime is the equivalent.
func getPidStatDataFromSysctl(pid int) (string, error) {
	for range 3 {
		before, err := unix.SysctlTimeval("kern.boottime")
		if err != nil {
			return "", err
		}
		start, _, err := kinfoStartTime(pid)
		if err != nil {
			return "", err
		}
		after, err := unix.SysctlTimeval("kern.boottime")
		if err != nil {
			return "", err
		}
		if before != after {
			continue // the clock was stepped meanwhile
		}
		rel := unix.NsecToTimeval(start.Nano() - after.Nano())
		return fmt.Sprintf("%s%d,%06d", bootRelativePrefix, rel.Sec, rel.Usec), nil
	}
	return "", fmt.Errorf("boot time kept changing while reading the start time of PID %d", pid)
}

// kinfoStartTime returns the process's ki_start (wall-clock) from kinfo_proc.
func kinfoStartTime(pid int) (unix.Timeval, *C.struct_kinfo_proc, error) {
	data, err := unix.SysctlRaw(sysctlName, pid)
	if err != nil {
		return unix.Timeval{}, nil, err
	}

	if len(data) != C.sizeof_struct_kinfo_proc {
		return unix.Timeval{}, nil, fmt.Errorf("incorrect read of %d bytes from %s sysctl", len(data), sysctlName)
	}

	kp := (*C.struct_kinfo_proc)(unsafe.Pointer(&data[0]))

	return unix.Timeval{Sec: int64(kp.ki_start.tv_sec), Usec: int64(kp.ki_start.tv_usec)}, kp, nil
}

// SetRuntimeUser sets the runtime user for the container.
func (c *Container) SetRuntimeUser(runtimeSpec *specs.Spec) {
	if runtimeSpec.Process == nil {
		logrus.Infof("Container %s is missing process attribute from the runtime specification", c.ID())

		return
	}

	user := runtimeSpec.Process.User
	supplementalGroups := make([]int64, 0, len(user.AdditionalGids))

	for _, gid := range user.AdditionalGids {
		supplementalGroups = append(supplementalGroups, int64(gid))
	}

	c.runtimeUser = &types.ContainerUser{
		Linux: &types.LinuxContainerUser{
			Uid:                int64(user.UID),
			Gid:                int64(user.GID),
			SupplementalGroups: supplementalGroups,
		},
	}
}

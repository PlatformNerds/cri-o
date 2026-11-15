//go:build !linux && !freebsd

package oci

func getPidStartTime(pid int) (string, error) {
	return "0", nil
}

// startTimesMatch reports whether a process start time saved for a
// container's PID matches the PID's current one (no PID reuse).
func startTimesMatch(saved, current string, _ int) bool {
	return saved == current
}

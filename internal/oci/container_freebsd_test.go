//go:build freebsd && cgo

package oci

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFreeBSDPidStartTime(t *testing.T) {
	pid := os.Getpid()
	st, err := getPidStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st, bootRelativePrefix) {
		t.Fatalf("start time %q is not boot-relative", st)
	}
	again, err := getPidStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !startTimesMatch(st, again, pid) {
		t.Errorf("start time not stable: %q vs %q", st, again)
	}
	// Containers created by older CRI-O versions saved the wall-clock time.
	abs, _, err := kinfoStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf("%d,%d", abs.Sec, abs.Usec)
	if !startTimesMatch(legacy, again, pid) {
		t.Errorf("legacy saved start time %q not accepted", legacy)
	}
	if startTimesMatch(bootRelativePrefix+"1,000000", again, pid) {
		t.Errorf("a different start time matched")
	}
	// microsecond jitter (boot time moving while ntpd slews) still matches,
	// 50 ms apart doesn't
	d, err := parseBootRelative(st)
	if err != nil {
		t.Fatal(err)
	}
	near := fmt.Sprintf("%s%d,%06d", bootRelativePrefix, (d+3*time.Microsecond)/time.Second, ((d+3*time.Microsecond)%time.Second)/time.Microsecond)
	far := fmt.Sprintf("%s%d,%06d", bootRelativePrefix, (d+50*time.Millisecond)/time.Second, ((d+50*time.Millisecond)%time.Second)/time.Microsecond)
	if !startTimesMatch(near, st, pid) {
		t.Errorf("%q and %q should match (3 us apart)", near, st)
	}
	if startTimesMatch(far, st, pid) {
		t.Errorf("%q and %q should not match (50 ms apart)", far, st)
	}
	if startTimesMatch("1,0", again, pid) {
		t.Errorf("a different legacy start time matched")
	}
}

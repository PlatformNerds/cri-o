//go:build freebsd && cgo

package oci

import (
	"fmt"
	"os"
	"strings"
	"testing"
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
	if startTimesMatch("1,0", again, pid) {
		t.Errorf("a different legacy start time matched")
	}
}

package device

import (
	"reflect"
	"testing"
)

func TestDevicesFromStringsFreeBSD(t *testing.T) {
	devs, err := devicesFromStrings([]string{"/dev/full", "/dev/null:/dev/null:rw", ""}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Device{
		{"/dev/full", "/dev/full", []string{"rule=path full unhide"}},
		{"/dev/null", "/dev/null", []string{"rule=path null unhide"}},
	}
	if !reflect.DeepEqual(devs, want) {
		t.Errorf("got %+v, want %+v", devs, want)
	}
	for _, bad := range []string{"/dev/full:/dev/other", "/tmp", "/dev", "/nonexistent", "/dev/full:x"} {
		if _, err := devicesFromStrings([]string{bad}, nil); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
	if _, err := devicesFromStrings([]string{"/dev/full"}, map[string]struct{}{"/dev/fuse": {}}); err == nil {
		t.Error("device not in allowed_devices: no error")
	}
	if devs, err := DevicesFromAnnotation("/dev/full,/dev/zero", []string{"/dev/full", "/dev/zero"}); err != nil || len(devs) != 2 {
		t.Errorf("annotation: %v %v", devs, err)
	}
}

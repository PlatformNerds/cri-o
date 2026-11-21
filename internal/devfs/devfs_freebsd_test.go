package devfs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	rspec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestTranslate(t *testing.T) {
	for _, tc := range []struct {
		src     Source
		dest    string
		want    *Translation
		wantErr bool
	}{
		{Source{".", true}, "/dev", &Translation{}, false},
		{Source{".", true}, "/dev/", &Translation{}, false},
		{Source{".", true}, "/host/dev", &Translation{Mount: true}, false},
		{Source{"fuse", false}, "/dev/fuse", &Translation{Rules: []string{"rule=path fuse unhide"}}, false},
		{Source{"dri/card0", false}, "/dev/dri/card0", &Translation{Rules: []string{
			"rule=path dri unhide", "rule=path dri/card0 unhide"}}, false},
		{Source{"dri", true}, "/dev/dri", &Translation{Rules: []string{
			"rule=path dri unhide", "rule=path dri/* unhide", "rule=path dri/*/* unhide", "rule=path dri/*/*/* unhide"}}, false},
		{Source{"klog", false}, "/dev/kmsg", nil, true},
		{Source{"klog", false}, "/var/kmsg", &Translation{Rules: []string{"rule=path klog unhide"}, Link: "/dev/klog"}, false},
		{Source{"md0", false}, "/mnt/volume1", &Translation{Rules: []string{"rule=path md0 unhide"}, Link: "/dev/md0"}, false},
		{Source{"dri", true}, "/host/dri", nil, true},
		{Source{"a b", false}, "/dev/a b", nil, true},
		{Source{"x*", false}, "/dev/x*", nil, true},
	} {
		got, err := Translate(&tc.src, "/dev/"+tc.src.Rel, tc.dest)
		if (err != nil) != tc.wantErr {
			t.Errorf("%+v -> %s: err %v, want error %v", tc.src, tc.dest, err, tc.wantErr)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%+v -> %s: got %+v, want %+v", tc.src, tc.dest, got, tc.want)
		}
	}
}

func TestHostSource(t *testing.T) {
	for _, tc := range []struct {
		path string
		want *Source
	}{
		{"/dev", &Source{".", true}},
		{"/dev/null", &Source{"null", false}},
		{"/tmp", nil},
		{"/nonexistent-devfs-test", nil},
	} {
		got, err := HostSource(tc.path)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, %v; want %+v", tc.path, got, err, tc.want)
		}
	}
}

func TestAddRulesAndSync(t *testing.T) {
	mounts := []rspec.Mount{
		{Destination: "/dev", Type: "devfs", Options: []string{"ruleset=4"}},
		{Destination: "/dev/fd", Type: "fdescfs"},
		NewMount("/host/dev"),
	}
	if err := AddRules(mounts, []string{"rule=path fuse unhide"}); err != nil {
		t.Fatal(err)
	}
	mounts[0].Options = append(mounts[0].Options, "rule=path shm unhide mode 1777")
	SyncMounts(mounts)
	want := []string{"ruleset=4", "rule=path fuse unhide", "rule=path shm unhide mode 1777"}
	if !reflect.DeepEqual(mounts[0].Options, want) || !reflect.DeepEqual(mounts[2].Options, want) {
		t.Errorf("got %v and %v, want %v", mounts[0].Options, mounts[2].Options, want)
	}
	if mounts[1].Options != nil {
		t.Errorf("fdescfs options changed: %v", mounts[1].Options)
	}
	// Changing one must not change the other.
	mounts[2].Options[0] = "x"
	if mounts[0].Options[0] != "ruleset=4" {
		t.Error("options are shared")
	}
	if err := AddRules([]rspec.Mount{{Destination: "/dev", Type: "nullfs"}}, []string{"r"}); err == nil {
		t.Error("AddRules without a devfs /dev: no error")
	}
}

func TestCreateLink(t *testing.T) {
	root := t.TempDir()
	if err := CreateLink(root, "/mnt/volume1", "/dev/md0"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(root, "mnt/volume1")); err != nil || got != "/dev/md0" {
		t.Errorf("got %q, %v", got, err)
	}
	// Again (a restarted container): the link is replaced.
	if err := CreateLink(root, "/mnt/volume1", "/dev/md1"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(root, "/data", "/dev/md0"); err == nil {
		t.Error("replaced a directory")
	}
	// A symlink in the image must not lead outside the root.
	if err := os.Symlink("/", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(root, "/escape/x", "/dev/md0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "x")); err != nil {
		t.Errorf("link not inside the root: %v", err)
	}
}

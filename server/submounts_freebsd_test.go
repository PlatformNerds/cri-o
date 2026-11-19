package server

import (
	"reflect"
	"testing"
)

func TestSelectSubmounts(t *testing.T) {
	host := []hostMount{
		{"/", "zfs"},
		{"/dev", "devfs"},
		{"/tmp", "zfs"},
		{"/var/log", "zfs"},
		{"/var/tmp", "zfs"},
		{"/var/db/containers", "zfs"},
		{"/var/db/containers/storage/zfs/graph/abc", "zfs"},
		{"/var/db/kubelet/pods/u1/volumes/kubernetes.io~secret/s", "tmpfs"},
		{"/var/run/ocijail/c1/readonly_root", "nullfs"},
		{"/var/log/deep/er", "tmpfs"},
		{"/usr/home", "zfs"},
	}
	exclude := []string{"/var/db/kubelet", "/var/db/containers/storage", "/var/run/ocijail"}

	type want struct{ src, dst, opt string }
	cases := []struct {
		name         string
		source, dest string
		readOnly     bool
		taken        []string
		want         []want
	}{
		{
			name: "hostPath /var, read-only", source: "/var", dest: "/host/var", readOnly: true,
			want: []want{
				{"/var/log", "/host/var/log", "ro"},
				{"/var/tmp", "/host/var/tmp", "ro"},
				{"/var/db/containers", "/host/var/db/containers", "ro"},
				{"/var/log/deep/er", "/host/var/log/deep/er", "ro"},
			},
		},
		{
			name: "hostPath / skips devfs and node-stack trees", source: "/", dest: "/rootfs",
			want: []want{
				{"/tmp", "/rootfs/tmp", "rw"},
				{"/usr/home", "/rootfs/usr/home", "rw"},
				{"/var/log", "/rootfs/var/log", "rw"},
				{"/var/tmp", "/rootfs/var/tmp", "rw"},
				{"/var/db/containers", "/rootfs/var/db/containers", "rw"},
				{"/var/log/deep/er", "/rootfs/var/log/deep/er", "rw"},
			},
		},
		{
			name: "a pod mount covers a submount", source: "/var", dest: "/v",
			taken: []string{"/v", "/v/log"},
			want: []want{
				{"/var/tmp", "/v/tmp", "rw"},
				{"/var/db/containers", "/v/db/containers", "rw"},
			},
		},
		{
			name: "the mount point itself is not a submount", source: "/var/log", dest: "/l",
			want: []want{{"/var/log/deep/er", "/l/deep/er", "rw"}},
		},
		{name: "nothing below", source: "/etc", dest: "/etc-host"},
		{name: "prefix is not a parent", source: "/var/lo", dest: "/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taken := map[string]struct{}{}
			for _, p := range tc.taken {
				taken[p] = struct{}{}
			}
			got := []want{}
			for _, m := range selectSubmounts(tc.source, tc.dest, tc.readOnly, exclude, taken, host) {
				if m.Type != "nullfs" || len(m.Options) != 1 {
					t.Fatalf("unexpected mount %+v", m)
				}
				got = append(got, want{m.Source, m.Destination, m.Options[0]})
			}
			w := tc.want
			if w == nil {
				w = []want{}
			}
			if !reflect.DeepEqual(got, w) {
				t.Errorf("got  %v\nwant %v", got, w)
			}
		})
	}
}

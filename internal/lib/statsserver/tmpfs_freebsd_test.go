package statsserver

import (
	"errors"
	"testing"
)

func TestPodTmpfsBytes(t *testing.T) {
	const mb = 1 << 20
	vol := "/var/db/kubelet/pods/u1/volumes/kubernetes.io~empty-dir/fill"
	tok := "/var/db/kubelet/pods/u1/volumes/kubernetes.io~projected/kube-api-access-x"
	fake := map[string]tmpfsMount{
		vol:          {fsid: [2]int32{1, 1}, mountPoint: vol, fsType: "tmpfs", usedBytes: 300 * mb},
		vol + "/sub": {fsid: [2]int32{1, 1}, mountPoint: vol, fsType: "tmpfs", usedBytes: 300 * mb},
		tok:          {fsid: [2]int32{2, 2}, mountPoint: tok, fsType: "tmpfs", usedBytes: 1 * mb},
		"/dev/shm":   {fsid: [2]int32{3, 3}, mountPoint: "/dev/shm", fsType: "tmpfs", usedBytes: 50 * mb},
		"/var/db/kubelet/pods/u1/volumes/kubernetes.io~empty-dir/disk": {fsid: [2]int32{4, 4}, mountPoint: "/", fsType: "zfs", usedBytes: 9000 * mb},
	}
	statfs := func(p string) (tmpfsMount, error) {
		m, ok := fake[p]
		if !ok {
			return tmpfsMount{}, errors.New("not found")
		}
		return m, nil
	}
	for _, tc := range []struct {
		name    string
		sources []string
		want    uint64
	}{
		{"none", nil, 0},
		{"memory emptyDir", []string{vol}, 300 * mb},
		{"same volume in two containers", []string{vol, vol}, 300 * mb},
		// a subPath source isn't the mount point; the volume itself is counted once
		{"subPath of the same volume", []string{vol + "/sub", vol}, 300 * mb},
		{"plus service account token", []string{vol, tok}, 301 * mb},
		{"host tmpfs via hostPath is not charged", []string{"/dev/shm"}, 0},
		{"disk-backed emptyDir is not memory", []string{"/var/db/kubelet/pods/u1/volumes/kubernetes.io~empty-dir/disk"}, 0},
		{"unreadable source ignored", []string{"/var/db/kubelet/pods/u1/volumes/kubernetes.io~empty-dir/gone"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := podTmpfsBytes(tc.sources, statfs); got != tc.want {
				t.Errorf("got %d MB, want %d MB", got/mb, tc.want/mb)
			}
		})
	}
}

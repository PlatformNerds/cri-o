package statsserver

import (
	"strings"

	"golang.org/x/sys/unix"
	types "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/cri-o/cri-o/internal/lib/sandbox"
)

// kubeletVolumeDir is in the host path of every volume the kubelet sets up
// for a pod (emptyDir, secret, configMap, projected, ...).
const kubeletVolumeDir = "/volumes/kubernetes.io~"

// tmpfsMount is what podTmpfsBytes needs to know about a mount source.
type tmpfsMount struct {
	fsid       [2]int32
	mountPoint string
	fsType     string
	usedBytes  uint64
}

// podTmpfsBytes returns the memory held by the pod's tmpfs volumes: on Linux
// tmpfs pages are charged to the cgroup of whoever wrote them, so the pod's
// memory usage includes them; on FreeBSD racct doesn't count tmpfs at all,
// so the kubelet saw a pod filling an emptyDir (medium: Memory) as nearly
// idle and, under memory pressure, could evict other pods first.
//
// Only kubelet volume mounts (source under .../volumes/kubernetes.io~<plugin>/)
// that are tmpfs mount points themselves count, each file system once (a
// volume may be mounted into several containers, or with subPath), so host
// tmpfs file systems shared through hostPath aren't charged to the pod.
func podTmpfsBytes(sources []string, statfs func(string) (tmpfsMount, error)) uint64 {
	seen := map[[2]int32]bool{}
	var total uint64
	for _, src := range sources {
		if !strings.Contains(src, kubeletVolumeDir) {
			continue
		}
		m, err := statfs(src)
		if err != nil || m.fsType != "tmpfs" || m.mountPoint != strings.TrimRight(src, "/") || seen[m.fsid] {
			continue
		}
		seen[m.fsid] = true
		total += m.usedBytes
	}
	return total
}

func statfsTmpfs(path string) (tmpfsMount, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return tmpfsMount{}, err
	}
	m := tmpfsMount{
		fsid:       st.Fsid.Val,
		mountPoint: unix.ByteSliceToString(st.Mntonname[:]),
		fsType:     unix.ByteSliceToString(st.Fstypename[:]),
	}
	if st.Blocks > st.Bfree {
		m.usedBytes = (st.Blocks - st.Bfree) * st.Bsize
	}
	return m, nil
}

// addPodTmpfsMemory charges the pod's tmpfs volumes to its memory usage.
func addPodTmpfsMemory(sb *sandbox.Sandbox, stats *types.PodSandboxStats) {
	mem := stats.GetLinux().GetMemory()
	if mem == nil {
		return
	}
	var sources []string
	for _, c := range sb.Containers().List() {
		spec := c.Spec()
		for _, m := range spec.Mounts {
			sources = append(sources, m.Source)
		}
	}
	tmpfs := podTmpfsBytes(sources, statfsTmpfs)
	if tmpfs == 0 {
		return
	}
	for _, v := range []*types.UInt64Value{mem.WorkingSetBytes, mem.UsageBytes} {
		if v != nil {
			v.Value += tmpfs
		}
	}
}

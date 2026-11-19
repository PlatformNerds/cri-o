package server

import (
	"path/filepath"
	"sort"
	"strings"

	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

// Recursive host path mounts.
//
// On Linux a hostPath volume is an rbind: the container also sees the file
// systems mounted below the path when it starts (with propagation None they
// are a snapshot; later mounts don't appear). A FreeBSD nullfs mount shows
// only the file system the path is on, so a pod with hostPath /var saw an
// empty /var/log when /var/log is its own ZFS dataset (the default layout),
// and hostPath / saw none of /tmp, /home, /var/log, ...
//
// hostSubmounts returns a nullfs mount for each file system mounted strictly
// below source, at the same place below destination, parents first.
//
// FreeBSD has no mount namespaces: a nullfs mount on top of a file system
// keeps it busy, and its own unmount then fails with EBUSY. Mounts that the
// node stack creates and removes while containers run are therefore skipped:
// the kubelet's pod volumes and plugin mounts, container root file systems,
// and runtime state. Pseudo file systems are skipped too; a nullfs view of
// the host's devfs would bypass the jail's devfs ruleset.
func hostSubmounts(source, destination string, readOnly bool, exclude []string, taken map[string]struct{}) []rspec.Mount {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil || n == 0 {
		return nil
	}
	buf := make([]unix.Statfs_t, n)
	if n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT); err != nil {
		return nil
	}
	found := make([]hostMount, 0, n)
	for i := range buf[:n] {
		found = append(found, hostMount{
			path:   unix.ByteSliceToString(buf[i].Mntonname[:]),
			fstype: unix.ByteSliceToString(buf[i].Fstypename[:]),
		})
	}
	return selectSubmounts(source, destination, readOnly, exclude, taken, found)
}

type hostMount struct{ path, fstype string }

var skippedSubmountTypes = map[string]bool{
	"devfs": true, "fdescfs": true, "procfs": true, "linprocfs": true,
	"linsysfs": true, "mqueuefs": true,
}

func selectSubmounts(source, destination string, readOnly bool, exclude []string, taken map[string]struct{}, found []hostMount) []rspec.Mount {
	source = filepath.Clean(source)
	destination = filepath.Clean(destination)
	seen := map[string]bool{}
	var paths []string
	for _, m := range found {
		p := filepath.Clean(m.path)
		if !isStrictlyUnder(p, source) || skippedSubmountTypes[m.fstype] || seen[p] {
			continue
		}
		skip := false
		for _, e := range exclude {
			if e != "" && (p == filepath.Clean(e) || isStrictlyUnder(p, filepath.Clean(e))) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	// Parents before children; a stable order otherwise.
	sort.Slice(paths, func(i, j int) bool {
		di, dj := strings.Count(paths[i], "/"), strings.Count(paths[j], "/")
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})

	opt := "rw"
	if readOnly {
		opt = "ro"
	}
	var mounts []rspec.Mount
	for _, p := range paths {
		rel, err := filepath.Rel(source, p)
		if err != nil {
			continue
		}
		dest := filepath.Join(destination, rel)
		// The pod mounts something of its own there or above it (below
		// this host path): that mount hides the submount.
		covered := false
		for t := range taken {
			t = filepath.Clean(t)
			if t == dest || (isStrictlyUnder(t, destination) && isStrictlyUnder(dest, t)) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		mounts = append(mounts, rspec.Mount{
			Type:        "nullfs",
			Source:      p,
			Destination: dest,
			Options:     []string{opt},
		})
	}
	return mounts
}

func isStrictlyUnder(p, dir string) bool {
	if dir == "/" {
		return p != "/" && strings.HasPrefix(p, "/")
	}
	return strings.HasPrefix(p, dir+"/")
}

// submountExcludes are the trees whose mounts come and go under the node
// stack: container storage, runtime state and the kubelet's directory.
func (s *Server) submountExcludes() []string {
	ex := []string{
		"/var/run/ocijail", "/var/run/crio", "/var/run/containers",
		"/var/db/kubelet", "/var/lib/kubelet",
	}
	if s.config.Root != "" {
		ex = append(ex, s.config.Root)
	}
	if s.config.RunRoot != "" {
		ex = append(ex, s.config.RunRoot)
	}
	return ex
}

// Package devfs maps host devices that a container asks for (hostPath
// volumes on the host's devfs, CRI devices) to the container's own devfs.
//
// A hostPath volume is a nullfs mount, and a nullfs view of the host's devfs
// shows every host device, whatever the container's devfs ruleset hides. The
// kernel refuses to open devices through nullfs (freebsd-src "nullfs: refuse
// to open device nodes"), so such a view is useless as well. On Linux an
// unprivileged container with hostPath /dev sees the host's device nodes, but
// the device cgroup lets it open only the devices it was given; a privileged
// container may open all of them.
//
// FreeBSD does the same with devfs itself:
//   - a device, or a directory of devices, at its own path below /dev in the
//     container is unhidden in the container's devfs;
//   - the root of the host's devfs (hostPath /dev) at another path, such as
//     /host/dev, becomes a second devfs mount with the ruleset and rules of
//     the container's /dev, so it shows what /dev shows;
//   - a device at another path (a block volume's devicePath) is unhidden at
//     its own name, and the path becomes a symbolic link to it;
//   - a directory at another path is an error: devfs can't show it under
//     another name.
//
// Rules applied with "devfs rule apply" cover the devices that exist when the
// container starts. A device created later (a driver loaded afterwards) stays
// hidden, like Linux's device nodes, which are created at container start too.
package devfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

// ContainerDev is where the container's devfs is mounted.
const ContainerDev = "/dev"

// dirDepth is how many levels below a device directory get unhidden: a devfs
// rule's "*" doesn't match "/".
const dirDepth = 3

// Source is a host path on a devfs.
type Source struct {
	// Rel is the path relative to the devfs mount point, "." for its root.
	Rel string
	Dir bool
}

// HostSource reports whether path (symbolic links already resolved) is on a
// devfs, and where. It returns nil when it isn't, or when path doesn't exist.
func HostSource(path string) (*Source, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return nil, nil
	}
	if unix.ByteSliceToString(st.Fstypename[:]) != "devfs" {
		return nil, nil
	}
	mnt := unix.ByteSliceToString(st.Mntonname[:])
	rel, err := filepath.Rel(mnt, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, fmt.Errorf("%s is on the devfs at %s, but not below it", path, mnt)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &Source{Rel: rel, Dir: fi.IsDir()}, nil
}

// Translation is what a host path on devfs becomes in the container.
type Translation struct {
	// Rules are "rule=..." options for the container's /dev devfs mount.
	Rules []string
	// Mount is true when the source is the root of the host's devfs and the
	// destination isn't /dev: mount another devfs there, with the options of
	// the container's /dev (see SyncMounts).
	Mount bool
	// Link is set when a device is wanted at another path (a block volume's
	// devicePath, e.g. /mnt/volume1): the destination becomes a symbolic link
	// to Link, the device in the container's devfs. Open, read, write and
	// ioctl behave as on the device; lstat(2) shows the link.
	Link string
}

// Translate maps src, a host path on devfs, to destination in the container.
func Translate(src *Source, hostPath, destination string) (*Translation, error) {
	destination = filepath.Clean(destination)
	if src.Rel == "." {
		if destination == ContainerDev {
			return &Translation{}, nil
		}
		return &Translation{Mount: true}, nil
	}
	want := filepath.Join(ContainerDev, src.Rel)
	if destination != want && (src.Dir || destination == ContainerDev || strings.HasPrefix(destination, ContainerDev+"/")) {
		// A link below /dev would be hidden by the container's devfs.
		return nil, fmt.Errorf("%s is on the host's devfs; on FreeBSD a container can only get it at %s, not %s", hostPath, want, destination)
	}
	rules, err := UnhideRules(src.Rel, src.Dir)
	if err != nil {
		return nil, err
	}
	t := &Translation{Rules: rules}
	if destination != want {
		// devfs can't show the device under another name: unhide it at its
		// own name and link the destination to it (see Link).
		t.Link = want
	}
	return t, nil
}

// UnhideRules returns the devfs rules that unhide rel (relative to the
// devfs root) and the directories above it; for a directory, also what is
// below it, dirDepth levels deep.
func UnhideRules(rel string, dir bool) ([]string, error) {
	if rel == "" || rel == "." || strings.ContainsAny(rel, "*?[]\\ \t\n,") {
		return nil, errors.New("unsupported devfs path " + fmt.Sprintf("%q", rel))
	}
	var rules []string
	parts := strings.Split(rel, "/")
	for i := range parts {
		rules = append(rules, "rule=path "+strings.Join(parts[:i+1], "/")+" unhide")
	}
	if dir {
		p := rel
		for range dirDepth {
			p += "/*"
			rules = append(rules, "rule=path "+p+" unhide")
		}
	}
	return rules, nil
}

// isContainerDev reports whether m is the container's /dev devfs mount.
func isContainerDev(m *rspec.Mount) bool {
	return m.Type == "devfs" && filepath.Clean(m.Destination) == ContainerDev
}

// AddRules appends rules to the options of the container's /dev devfs mount.
func AddRules(mounts []rspec.Mount, rules []string) error {
	if len(rules) == 0 {
		return nil
	}
	for i := range mounts {
		if isContainerDev(&mounts[i]) {
			mounts[i].Options = append(mounts[i].Options, rules...)
			return nil
		}
	}
	return errors.New("the container has no devfs at " + ContainerDev + " to show host devices in")
}

// NewMount returns a devfs mount at destination; SyncMounts sets its options.
func NewMount(destination string) rspec.Mount {
	return rspec.Mount{Destination: destination, Type: "devfs", Source: "devfs"}
}

// SyncMounts gives every other devfs mount the options of the container's
// /dev: its ruleset (ruleset=0 for privileged containers) and the rules
// added to it. Call it once nothing changes /dev's options any more.
func SyncMounts(mounts []rspec.Mount) {
	var opts []string
	found := false
	for i := range mounts {
		if isContainerDev(&mounts[i]) {
			opts, found = mounts[i].Options, true
			break
		}
	}
	for i := range mounts {
		if mounts[i].Type == "devfs" && !isContainerDev(&mounts[i]) {
			if found {
				mounts[i].Options = append([]string(nil), opts...)
			} else if len(mounts[i].Options) == 0 {
				// No /dev to copy from: the default jail ruleset, as /dev has.
				mounts[i].Options = []string{"ruleset=4"}
			}
		}
	}
}

// CreateLink makes the symbolic link at destination (a path in the
// container) to target, in the container's root file system at rootfs. An
// existing link is replaced; anything else at destination is an error.
func CreateLink(rootfs, destination, target string) error {
	parent, err := securejoin.SecureJoin(rootfs, filepath.Dir(destination))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	link := filepath.Join(parent, filepath.Base(destination))
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("cannot link device %s to %s: something else is there", target, destination)
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	return os.Symlink(target, link)
}

// TranslateHostPath resolves hostPath (the kubelet hands block volumes over
// as a link to the device) and translates it for destination. It returns nil
// when hostPath isn't on a devfs.
func TranslateHostPath(hostPath, destination string) (*Translation, error) {
	resolved, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		return nil, nil
	}
	src, err := HostSource(resolved)
	if err != nil || src == nil {
		return nil, err
	}
	return Translate(src, hostPath, destination)
}

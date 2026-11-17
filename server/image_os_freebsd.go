package server

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"

	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	types "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/cri-o/cri-o/internal/storage"
)

// ImageOSAnnotation, set in a runtime handler's default_annotations, selects
// the operating system of the images pulled for pods using that handler. On
// FreeBSD, a handler with "io.bsdk8s.image-os" = "linux" (exposed through a
// Kubernetes RuntimeClass) runs Linux images in jails under the Linux ABI
// (Linuxulator): their ELF binaries run directly on the FreeBSD kernel.
//
//	[crio.runtime.runtimes.linux]
//	runtime_path = "/usr/local/bin/ocijail"
//	default_annotations = { "io.bsdk8s.image-os" = "linux" }
const ImageOSAnnotation = "io.bsdk8s.image-os"

// imageOSForPod returns the image OS for a pull on behalf of a pod: the
// kubelet creates the pod's sandbox before pulling its images, so the
// sandbox's runtime handler is known (the CRI only passes the handler with
// pulls behind the alpha RuntimeClassInImageCriApi gate).
func (s *Server) imageOSForPod(sc *types.PodSandboxConfig) string {
	uid := sc.GetMetadata().GetUid()
	if uid == "" {
		return ""
	}
	for _, sb := range s.ContainerServer.ListSandboxes() {
		if sb.Metadata().GetUid() != uid {
			continue
		}
		if h, ok := s.config.Runtimes[sb.RuntimeHandler()]; ok && h != nil {
			return h.DefaultAnnotations[ImageOSAnnotation]
		}
	}
	return ""
}

// addImageOSMounts gives containers of Linux images the Linux views of /proc
// and /sys and a /dev/shm, as podman does for Linux containers on FreeBSD.
// Mounts the pod supplies itself win.
func addImageOSMounts(specgen *generate.Generator, img *storage.ImageResult, containerConfig *types.ContainerConfig) {
	if img == nil || img.OCIConfig == nil || img.OCIConfig.OS != "linux" {
		return
	}
	have := map[string]bool{}
	for _, m := range containerConfig.GetMounts() {
		have[filepath.Clean(m.GetContainerPath())] = true
	}
	mounts := specgen.Mounts()
	for i, m := range mounts {
		have[filepath.Clean(m.Destination)] = true
		// devfs has a /dev/shm directory (the tmpfs mount point below),
		// but the jail's devfs ruleset hides it, and nothing can be created
		// in devfs: unhide it, as podman does for Linux containers.
		if filepath.Clean(m.Destination) == "/dev" && m.Type == "devfs" && !have["/dev/shm"] {
			mounts[i].Options = append(mounts[i].Options, "rule=path shm unhide mode 1777")
		}
	}
	specgen.ClearMounts()
	for _, m := range mounts {
		specgen.AddMount(m)
	}
	addedSys := false
	for _, m := range linuxImageMounts {
		if !have[m.Destination] {
			specgen.AddMount(m)
			addedSys = addedSys || m.Destination == "/sys"
		}
	}
	// A cgroup v2 view of the container's limits, so that Linux runtimes
	// (JVM, Go, .NET, Node) size themselves to the pod and not to the host.
	// Only on our /sys (it provides the mount point) and when the kernel
	// has the file system.
	if addedSys && !have["/sys/fs/cgroup"] && linuxCgroupfsSupported() {
		specgen.AddMount(rspec.Mount{
			Destination: "/sys/fs/cgroup", Type: "lincgroupfs", Source: "cgroup2",
			Options: []string{"ro", "nodev", "nosuid", "noexec"},
		})
	}
}

// linuxCgroupfsSupported reports whether the kernel provides lincgroupfs
// (kern.features.linux_cgroupfs, from the linsysfs module).
func linuxCgroupfsSupported() bool {
	v, err := unix.SysctlUint32("kern.features.linux_cgroupfs")
	return err == nil && v == 1
}

var linuxImageMounts = []rspec.Mount{
	{Destination: "/proc", Type: "linprocfs", Source: "proc", Options: []string{"nodev"}},
	{Destination: "/sys", Type: "linsysfs", Source: "sysfs", Options: []string{"nodev"}},
	{Destination: "/dev/shm", Type: "tmpfs", Source: "shm", Options: []string{"mode=1777"}},
}

// imageOSFallbacks: images of the Linux RuntimeClass are stored as Linux
// instances; see imageStatusByName.
func imageOSFallbacks() []string { return []string{"linux"} }

// checkImageOS refuses to create a container of a handler that declares an
// image OS (ImageOSAnnotation) from an image of another OS. The kubelet asks
// whether an image is present by name only, so when the local copy of a tag
// is a FreeBSD image, a pod of the Linux handler would otherwise silently run
// FreeBSD binaries (and the reverse).
func (s *Server) checkImageOS(handler, userRequestedImage string, img *storage.ImageResult) error {
	h, ok := s.config.Runtimes[handler]
	if !ok || h == nil {
		return nil
	}

	want := h.DefaultAnnotations[ImageOSAnnotation]
	if want == "" || img == nil || img.OCIConfig == nil || img.OCIConfig.OS == "" {
		return nil
	}

	if img.OCIConfig.OS != want {
		return fmt.Errorf("image %q (%s) is a %s image, but runtime handler %q runs %s images; "+
			"the local copy of this tag is for another OS, remove it or use a %s image",
			userRequestedImage, img.ID, img.OCIConfig.OS, handler, want, want)
	}

	return nil
}

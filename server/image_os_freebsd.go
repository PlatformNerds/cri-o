package server

import (
	"path/filepath"

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
	for _, m := range specgen.Mounts() {
		have[filepath.Clean(m.Destination)] = true
	}
	for _, m := range linuxImageMounts {
		if !have[m.Destination] {
			specgen.AddMount(m)
		}
	}
}

var linuxImageMounts = []rspec.Mount{
	{Destination: "/proc", Type: "linprocfs", Source: "proc", Options: []string{"nodev"}},
	{Destination: "/sys", Type: "linsysfs", Source: "sysfs", Options: []string{"nodev"}},
	{Destination: "/dev/shm", Type: "tmpfs", Source: "shm", Options: []string{"mode=1777"}},
}

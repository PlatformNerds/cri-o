package server

import (
	"strings"

	imagetypes "go.podman.io/image/v5/types"

	pkgstorage "github.com/cri-o/cri-o/internal/storage"
)

// imageStatusByName resolves an image name in local storage. A name that
// refers to a multi-platform index (e.g. "repo:tag@sha256:<index digest>")
// is resolved to the instance for the host's OS. On FreeBSD, images pulled
// for the Linux RuntimeClass are Linux instances, so that fails with "no
// image found in image index ... OS freebsd" although the image is present;
// retry with each fallback OS (imageOSFallbacks) before giving up.
func (s *Server) imageStatusByName(name pkgstorage.RegistryImageReference) (*pkgstorage.ImageResult, error) {
	status, err := s.ContainerServer.StorageImageServer().ImageStatusByName(s.config.SystemContext, name)
	if err == nil || !isNoInstanceForPlatform(err) {
		return status, err
	}
	for _, os := range imageOSFallbacks() {
		sc := imagetypes.SystemContext{}
		if s.config.SystemContext != nil {
			sc = *s.config.SystemContext
		}
		sc.OSChoice = os
		if st, ferr := s.ContainerServer.StorageImageServer().ImageStatusByName(&sc, name); ferr == nil {
			return st, nil
		}
	}
	return status, err
}

func isNoInstanceForPlatform(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no image found in image index for") ||
		strings.Contains(msg, "no image found in manifest list for")
}

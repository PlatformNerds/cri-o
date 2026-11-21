//go:build !freebsd

package server

import (
	"github.com/opencontainers/runtime-tools/generate"
	types "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/cri-o/cri-o/internal/storage"
)

// imageOSForPod: images are pulled for the host's OS.
func (s *Server) imageOSForPod(_ *types.PodSandboxConfig) string { return "" }

// addImageOSMounts: nothing to add for native images.
func addImageOSMounts(_ *generate.Generator, _ *storage.ImageResult, _ *types.ContainerConfig) {}

// imageOSFallbacks: images are always resolved for the host's OS.
func imageOSFallbacks() []string { return nil }

// checkImageOS: there is no per-handler image OS on this platform.
func (s *Server) checkImageOS(_, _ string, _ *storage.ImageResult) error { return nil }

func finishDevfsMounts(_ *generate.Generator) {}

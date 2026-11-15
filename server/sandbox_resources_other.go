//go:build !freebsd

package server

import (
	"context"

	types "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/cri-o/cri-o/internal/lib/sandbox"
)

// updatePodLevelLimits: elsewhere the kubelet applies pod-level limits to
// the pod cgroup itself; the runtime is only notified (NRI).
func (s *Server) updatePodLevelLimits(_ context.Context, _ *sandbox.Sandbox, _ *types.LinuxContainerResources) error {
	return nil
}

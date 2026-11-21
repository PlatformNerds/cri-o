package server

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cri-o/cri-o/internal/devfs"
	ctrfactory "github.com/cri-o/cri-o/internal/factory/container"
	"github.com/cri-o/cri-o/internal/log"
)

// createDeviceLinks makes, in the container's root file system, the links to
// devices wanted at another path than their own (see devfs.Translation.Link):
// the CRI devices of block volumes, and host path volumes of a device.
func (s *Server) createDeviceLinks(ctx context.Context, ctr ctrfactory.Container, mountPoint string) error {
	type want struct{ hostPath, destination string }
	var wants []want
	for _, d := range ctr.Config().GetDevices() {
		wants = append(wants, want{d.GetHostPath(), d.GetContainerPath()})
	}
	for _, m := range ctr.Config().GetMounts() {
		if m.GetHostPath() == "" || m.GetImage().GetImage() != "" {
			continue
		}
		wants = append(wants, want{filepath.Join(s.config.BindMountPrefix, m.GetHostPath()), m.GetContainerPath()})
	}
	for _, w := range wants {
		t, err := devfs.TranslateHostPath(w.hostPath, w.destination)
		if err != nil || t == nil || t.Link == "" {
			// Errors were reported when the spec was made.
			continue
		}
		if err := devfs.CreateLink(mountPoint, w.destination, t.Link); err != nil {
			return fmt.Errorf("device %s: %w", w.hostPath, err)
		}
		log.Infof(ctx, "Device %s: %s links to %s", w.hostPath, w.destination, t.Link)
	}
	return nil
}

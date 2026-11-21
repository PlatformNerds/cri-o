//go:build !freebsd

package server

import (
	"context"

	ctrfactory "github.com/cri-o/cri-o/internal/factory/container"
)

func (s *Server) createDeviceLinks(context.Context, ctrfactory.Container, string) error { return nil }

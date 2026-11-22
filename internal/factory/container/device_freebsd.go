package container

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"

	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"tags.cncf.io/container-device-interface/pkg/cdi"
	cdispecs "tags.cncf.io/container-device-interface/specs-go"

	"github.com/cri-o/cri-o/internal/devfs"
	"github.com/cri-o/cri-o/internal/log"

	devicecfg "github.com/cri-o/cri-o/internal/config/device"
)

func (c *container) SpecAddDevices(configuredDevices, annotationDevices []devicecfg.Device, privilegedWithoutHostDevices, enableDeviceOwnershipFromSecurityContext bool) error {
	// additional_devices from the config, then the devices of the
	// io.kubernetes.cri-o.Devices annotation: unhide them in the
	// container's devfs (parsed and checked in internal/config/device).
	for _, devs := range [][]devicecfg.Device{configuredDevices, annotationDevices} {
		for i := range devs {
			if err := devfs.AddRules(c.Spec().Config.Mounts, devs[i].Rules); err != nil {
				return fmt.Errorf("device %s: %w", devs[i].HostPath, err)
			}
		}
	}

	// Then, add host devices if privileged
	if err := c.specAddHostDevicesIfPrivileged(privilegedWithoutHostDevices); err != nil {
		return err
	}

	// Then, add container config devices
	if err := c.specAddContainerConfigDevices(enableDeviceOwnershipFromSecurityContext); err != nil {
		return err
	}

	// Finally, inject CDI devices
	return c.specInjectCDIDevices()
}

func (c *container) specAddHostDevicesIfPrivileged(privilegedWithoutHostDevices bool) error {
	if !c.Privileged() || privilegedWithoutHostDevices {
		return nil
	}

	// Modify the devfs mount to add all devices
	for i, m := range c.Spec().Config.Mounts {
		if m.Type == "devfs" && filepath.Clean(m.Destination) == devfs.ContainerDev {
			m.Options = []string{"ruleset=0"}
			c.Spec().Config.Mounts[i] = m
			return nil
		}
	}
	return nil
}

// specAddContainerConfigDevices unhides the CRI devices (device plugins,
// block volumes) in the container's devfs. A device wanted at another path
// (a block volume's devicePath) also gets a link there, made once the root
// file system is mounted (see devfs.Translation.Link). Privileged containers
// see every device already.
func (c *container) specAddContainerConfigDevices(enableDeviceOwnershipFromSecurityContext bool) error {
	for _, d := range c.Config().GetDevices() {
		t, err := devfs.TranslateHostPath(d.GetHostPath(), d.GetContainerPath())
		if err != nil {
			return fmt.Errorf("device %s: %w", d.GetHostPath(), err)
		}
		if t == nil || t.Mount {
			return fmt.Errorf("device %s: not a device on the host's devfs", d.GetHostPath())
		}
		if c.Privileged() {
			continue
		}
		if err := devfs.AddRules(c.Spec().Config.Mounts, t.Rules); err != nil {
			return fmt.Errorf("device %s: %w", d.GetHostPath(), err)
		}
	}
	return nil
}

func (c *container) specInjectCDIDevices() error {
	return nil
}

// SpecInjectCDIDevices injects the CDI devices of the CRI config and of the
// cdi.k8s.io annotations (DRA drivers, device plugins), as on Linux. CDI's
// environment variables, mounts and hooks apply as they are. Its device
// nodes don't: a FreeBSD container sees devices through its devfs, so each
// host device node is unhidden there by a devfs rule, at its own path. The
// Linux-only results (spec.linux devices, device cgroup rules) are dropped,
// and network devices and Intel RDT are refused.
func (c *container) SpecInjectCDIDevices() error {
	requested, err := c.requestedCDIDevices()
	if err != nil || len(requested) == 0 {
		return err
	}

	if err := cdi.Refresh(); err != nil {
		// As on Linux: a broken spec of one vendor shouldn't stop the
		// others; injection fails below if a requested device is affected.
		log.Warnf(context.TODO(), "CDI registry has errors: %v", err)
	}

	cache := cdi.GetDefaultCache()
	var nodes []*cdispecs.DeviceNode
	seenSpec := map[*cdi.Spec]bool{}
	for _, name := range requested {
		d := cache.GetDevice(name)
		if d == nil {
			continue // InjectDevices reports it
		}
		nodes = append(nodes, d.ContainerEdits.DeviceNodes...)
		if s := d.GetSpec(); s != nil && !seenSpec[s] {
			seenSpec[s] = true
			nodes = append(nodes, s.ContainerEdits.DeviceNodes...)
			if s.ContainerEdits.IntelRdt != nil || len(s.ContainerEdits.NetDevices) > 0 {
				return fmt.Errorf("CDI device %s: network devices and Intel RDT are not supported on %s", name, runtime.GOOS)
			}
		}
		if d.ContainerEdits.IntelRdt != nil || len(d.ContainerEdits.NetDevices) > 0 {
			return fmt.Errorf("CDI device %s: network devices and Intel RDT are not supported on %s", name, runtime.GOOS)
		}
	}

	spec := c.Spec().Config
	saved := snapshotLinux(spec.Linux)
	if _, err := cdi.InjectDevices(spec, requested...); err != nil {
		return fmt.Errorf("CDI device injection failed: %w", err)
	}
	spec.Linux = saved

	var rules []string
	for _, n := range nodes {
		if n == nil {
			continue
		}
		hostPath := n.HostPath
		if hostPath == "" {
			hostPath = n.Path
		}
		t, err := devfs.TranslateHostPath(hostPath, n.Path)
		if err != nil {
			return fmt.Errorf("CDI device node %s: %w", hostPath, err)
		}
		if t == nil || t.Mount {
			return fmt.Errorf("CDI device node %s: not a device on the host's devfs", hostPath)
		}
		if t.Link != "" {
			return fmt.Errorf("CDI device node %s: on %s a container gets it at %s, not %s", hostPath, runtime.GOOS, t.Link, n.Path)
		}
		rules = append(rules, t.Rules...)
	}
	if len(rules) == 0 || c.Privileged() {
		return nil
	}
	if err := devfs.AddRules(spec.Mounts, rules); err != nil {
		return err
	}
	// Other devfs mounts (host path volumes of /dev) follow /dev's rules.
	devfs.SyncMounts(spec.Mounts)
	return nil
}

// requestedCDIDevices returns the CDI devices of the CRI config, then those
// of the annotations that the CRI field doesn't name already.
func (c *container) requestedCDIDevices() ([]string, error) {
	fromCRI := map[string]bool{}
	var requested []string
	for _, dev := range c.Config().GetCDIDevices() {
		requested = append(requested, dev.GetName())
		fromCRI[dev.GetName()] = true
	}
	_, annotated, err := cdi.ParseAnnotations(c.Config().GetAnnotations())
	if err != nil {
		return nil, fmt.Errorf("failed to parse CDI device annotations: %w", err)
	}
	for _, name := range annotated {
		if !fromCRI[name] {
			requested = append(requested, name)
		}
	}
	return requested, nil
}

// snapshotLinux copies the parts of spec.linux CDI injection changes.
func snapshotLinux(l *rspec.Linux) *rspec.Linux {
	if l == nil {
		return nil
	}
	s := *l
	s.Devices = slices.Clone(l.Devices)
	if l.Resources != nil {
		r := *l.Resources
		r.Devices = slices.Clone(l.Resources.Devices)
		s.Resources = &r
	}
	return &s
}

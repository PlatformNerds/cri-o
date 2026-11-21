package container

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/cri-o/cri-o/internal/devfs"

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

// specAddContainerConfigDevices unhides the CRI devices (from device
// plugins, for example) in the container's devfs. devfs can't show a device
// under another name, so the container path must be the device's own path.
// Privileged containers already see every device.
func (c *container) specAddContainerConfigDevices(enableDeviceOwnershipFromSecurityContext bool) error {
	if c.Privileged() {
		return nil
	}
	for _, d := range c.Config().GetDevices() {
		src, err := devfs.HostSource(d.GetHostPath())
		if err != nil {
			return fmt.Errorf("device %s: %w", d.GetHostPath(), err)
		}
		if src == nil || src.Rel == "." {
			return fmt.Errorf("device %s: not a device on the host's devfs", d.GetHostPath())
		}
		t, err := devfs.Translate(src, d.GetHostPath(), d.GetContainerPath())
		if err != nil {
			return err
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

func (c *container) SpecInjectCDIDevices() error {
	if len(c.Config().CDIDevices) > 0 {
		return fmt.Errorf("(*container).SpecInjectCDIDevices not supported on %s", runtime.GOOS)
	}
	return nil
}

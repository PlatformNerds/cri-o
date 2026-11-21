package device

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cri-o/cri-o/internal/devfs"
)

// DeviceAnnotationDelim is the character used to separate devices in the
// annotation `io.kubernetes.cri-o.Devices`.
const DeviceAnnotationDelim = ","

// Config holds the devices of additional_devices, given to all containers.
type Config struct {
	devices []Device
}

// Device is a host device for a container. A jail sees devices through its
// devfs, so the device is unhidden there (see package devfs): it keeps its
// name, and the container path must be its own path below /dev.
type Device struct {
	HostPath      string
	ContainerPath string
	// Rules are the devfs rules that unhide the device.
	Rules []string
}

// New creates a new device Config.
func New() *Config {
	return &Config{devices: make([]Device, 0)}
}

// LoadDevices parses additional_devices.
func (d *Config) LoadDevices(devsFromConfig []string) error {
	devs, err := devicesFromStrings(devsFromConfig, nil)
	if err != nil {
		return err
	}
	d.devices = devs
	return nil
}

// Devices returns the devices saved in the Config.
func (d *Config) Devices() []Device {
	return d.devices
}

// DevicesFromAnnotation parses io.kubernetes.cri-o.Devices
// ($PATH{:$PATH}{:$MODE},...), allowing only allowedDevices.
func DevicesFromAnnotation(annotation string, allowedDevices []string) ([]Device, error) {
	allowedMap := make(map[string]struct{})
	for _, d := range allowedDevices {
		allowedMap[d] = struct{}{}
	}
	return devicesFromStrings(strings.Split(annotation, DeviceAnnotationDelim), allowedMap)
}

// devicesFromStrings parses $PATH{:$PATH}{:$MODE} entries. The mode is
// accepted for compatibility; devfs has no per-container access modes, the
// device node's own mode applies.
func devicesFromStrings(devs []string, allowedDevices map[string]struct{}) ([]Device, error) {
	res := make([]Device, 0, len(devs))
	for _, d := range devs {
		if d == "" {
			continue
		}
		src, dst, _, err := parseDevice(d)
		if err != nil {
			return nil, err
		}
		if allowedDevices != nil {
			if _, ok := allowedDevices[src]; !ok {
				return nil, fmt.Errorf("device %s is specified but is not in allowed_devices", src)
			}
		}
		resolved, err := filepath.EvalSymlinks(src)
		if err != nil {
			return nil, fmt.Errorf("%s is not a valid device: %w", src, err)
		}
		ds, err := devfs.HostSource(resolved)
		if err != nil {
			return nil, fmt.Errorf("%s is not a valid device: %w", src, err)
		}
		if ds == nil || ds.Rel == "." {
			return nil, fmt.Errorf("%s is not a device on the host's devfs", src)
		}
		t, err := devfs.Translate(ds, src, dst)
		if err != nil {
			return nil, err
		}
		if t.Link != "" {
			return nil, fmt.Errorf("%s: on FreeBSD a configured device keeps its own path (%s), not %s", src, t.Link, dst)
		}
		res = append(res, Device{HostPath: src, ContainerPath: dst, Rules: t.Rules})
	}
	return res, nil
}

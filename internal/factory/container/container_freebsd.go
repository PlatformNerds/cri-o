package container

import (
	"strings"

	"github.com/moby/sys/capability"
)

func (c *container) SelinuxLabel(sboxLabel string) ([]string, error) {
	return []string{}, nil
}

// capabilitiesEnforced is false: jails have no capabilities, so none are put
// in the OCI spec (the generator would reject them as unsupported by the
// host).
const capabilitiesEnforced = false

// getOCICapabilitiesList returns the Linux capability names a pod may add or
// drop. FreeBSD has no capabilities and jails don't enforce them: root in a
// jail keeps root's (jail-restricted) privileges and other users have none.
// Accepting the names lets unmodified manifests that add/drop capabilities
// (e.g. NET_BIND_SERVICE for a non-root web server) run; rejecting them made
// container creation fail with "unknown capability". The one capability
// commonly needed by non-root workloads, binding ports below 1024, is
// provided per pod instead via the net.ipv4.ip_unprivileged_port_start sysctl
// (see default_sysctls), which ocijail maps to the pod vnet's
// net.inet.ip.portrange.reservedhigh.
func getOCICapabilitiesList() ([]string, error) {
	known := capability.ListKnown()
	caps := make([]string, 0, len(known))
	for _, c := range known {
		caps = append(caps, "CAP_"+strings.ToUpper(c.String()))
	}
	return caps, nil
}

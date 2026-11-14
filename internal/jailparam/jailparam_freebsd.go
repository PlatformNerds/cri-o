// Package jailparam has helpers for FreeBSD jail parameters that CRI-O passes
// to the OCI runtime (ocijail) as org.freebsd.jail.* annotations.
package jailparam

import "golang.org/x/sys/unix"

// AllowPFAnnotation asks ocijail to set the allow.pf jail parameter, which
// lets a non-VNET (host network) jail manage the host pf firewall.
const AllowPFAnnotation = "org.freebsd.jail.allow.pf"

// Supported reports whether the running kernel knows the jail parameter
// name (e.g. "allow.pf"), i.e. security.jail.param.<name> exists. Setting an
// unknown parameter makes jail_set(2) fail, so optional parameters must only
// be requested when supported.
func Supported(name string) bool {
	_, err := unix.SysctlRaw("security.jail.param." + name)
	return err == nil
}

// HostNetworkPFAllowed reports whether a host-network jail that is privileged
// should get allow.pf: the FreeBSD analogue of CAP_NET_ADMIN for e.g.
// kube-proxy (pf mode) or network policy agents, which manage the host pf.
func HostNetworkPFAllowed(hostNetwork, privileged bool) bool {
	return hostNetwork && privileged && Supported("allow.pf")
}

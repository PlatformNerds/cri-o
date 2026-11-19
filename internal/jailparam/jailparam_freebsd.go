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

// AllowNFSDAnnotation asks ocijail to set the allow.nfsd jail parameter:
// mountd(8) and nfsd(8) may run in the jail.
const AllowNFSDAnnotation = "org.freebsd.jail.allow.nfsd"

// PodNFSDAllowed reports whether a privileged jail of a pod with its own
// network should get allow.nfsd, so that a privileged container can serve NFS
// as a privileged Linux container can run the kernel NFS server (e.g. the
// Kubernetes e2e NFS server, NFS provisioners). The pod's containers inherit
// the pod jail's vnet; the kernel lets them run nfsd only with
// security.jail.nfsd_inherited_vnet=1 (freebsd-src patch 0036). Without it
// the parameter is harmless: the daemons fail as before.
func PodNFSDAllowed(hostNetwork, privileged bool) bool {
	return !hostNetwork && privileged && Supported("allow.nfsd")
}

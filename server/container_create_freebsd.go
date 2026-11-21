package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"go.podman.io/storage/pkg/idtools"
	"golang.org/x/net/context"
	types "k8s.io/cri-api/pkg/apis/runtime/v1"
	crierrors "k8s.io/cri-api/pkg/errors"

	"github.com/cri-o/cri-o/internal/config/device"
	"github.com/cri-o/cri-o/internal/devfs"
	ctrfactory "github.com/cri-o/cri-o/internal/factory/container"
	"github.com/cri-o/cri-o/internal/jailparam"
	"github.com/cri-o/cri-o/internal/lib/sandbox"
	"github.com/cri-o/cri-o/internal/log"
	oci "github.com/cri-o/cri-o/internal/oci"
	"github.com/cri-o/cri-o/internal/storage"
	"github.com/cri-o/cri-o/pkg/annotations/v2"
)

// finalizeUserMapping changes the UID, GID and additional GIDs to reflect the new value in the user namespace.
func (s *Server) finalizeUserMapping(sb *sandbox.Sandbox, specgen *generate.Generator, mappings *idtools.IDMappings) {
}

// this function takes a container config and makes sure its SecurityContext
// is not nil. If it is, it makes sure to set default values for every field.
func setContainerConfigSecurityContext(containerConfig *types.ContainerConfig) *types.LinuxContainerSecurityContext {
	if containerConfig.GetLinux() == nil {
		containerConfig.Linux = &types.LinuxContainerConfig{}
	}

	if containerConfig.GetLinux().GetSecurityContext() == nil {
		containerConfig.Linux.SecurityContext = newLinuxContainerSecurityContext()
	}

	if containerConfig.GetLinux().GetSecurityContext().GetNamespaceOptions() == nil {
		containerConfig.Linux.SecurityContext.NamespaceOptions = &types.NamespaceOption{}
	}

	if containerConfig.GetLinux().GetSecurityContext().GetSelinuxOptions() == nil {
		containerConfig.Linux.SecurityContext.SelinuxOptions = &types.SELinuxOption{}
	}

	return containerConfig.GetLinux().GetSecurityContext()
}

func disableFipsForContainer(ctr ctrfactory.Container, containerDir string) error {
	return nil
}

func addSysfsMounts(ctr ctrfactory.Container, containerConfig *types.ContainerConfig, hostNet, usernsEnabled bool) {
}

func setOCIBindMountsPrivileged(g *generate.Generator) {
}

func (s *Server) addOCIBindMounts(ctx context.Context, ctr ctrfactory.Container, ctrInfo *storage.ContainerInfo, maybeRelabel, skipRelabel, cgroup2RW, idMapSupport, rroSupport bool) ([]oci.ContainerVolume, []rspec.Mount, []*safeMountInfo, error) {
	ctx, span := log.StartSpan(ctx)
	defer span.End()

	volumes := []oci.ContainerVolume{}
	ociMounts := []rspec.Mount{}
	containerConfig := ctr.Config()
	specgen := ctr.Spec()
	mounts := containerConfig.Mounts

	// Sort mounts in number of parts. This ensures that high level mounts don't
	// shadow other mounts.
	sort.Sort(criOrderedMounts(mounts))

	// Copy all mounts from default mounts, except for
	// - mounts overridden by supplied mount;
	// - all mounts under /dev if a supplied /dev is present.
	// Host paths on the host's devfs become devfs rules or mounts; see
	// package devfs. Find them first: a hostPath /dev at /dev must not
	// replace the container's own devfs.
	devfsMounts := map[*types.Mount]*devfs.Translation{}
	for _, m := range mounts {
		if m.HostPath == "" || m.GetImage().GetImage() != "" {
			continue
		}
		src := filepath.Join(s.config.BindMountPrefix, m.HostPath)
		if resolved, err := resolveSymbolicLink(s.config.BindMountPrefix, src); err == nil {
			src = resolved
		}
		ds, err := devfs.HostSource(src)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("host path %s: %w", m.HostPath, err)
		}
		if ds == nil {
			continue
		}
		t, err := devfs.Translate(ds, m.HostPath, m.ContainerPath)
		if err != nil {
			return nil, nil, nil, err
		}
		devfsMounts[m] = t
	}

	mountSet := make(map[string]struct{})
	for _, m := range mounts {
		if _, ok := devfsMounts[m]; ok {
			continue
		}
		mountSet[filepath.Clean(m.ContainerPath)] = struct{}{}
	}
	defaultMounts := specgen.Mounts()
	specgen.ClearMounts()
	for _, m := range defaultMounts {
		dst := filepath.Clean(m.Destination)
		if _, ok := mountSet[dst]; ok {
			// filter out mount overridden by a supplied mount
			continue
		}
		if _, mountDev := mountSet["/dev"]; mountDev && strings.HasPrefix(dst, "/dev/") {
			// filter out everything under /dev if /dev is a supplied mount
			continue
		}
		if _, mountSys := mountSet["/sys"]; mountSys && strings.HasPrefix(dst, "/sys/") {
			// filter out everything under /sys if /sys is a supplied mount
			continue
		}
		specgen.AddMount(m)
	}

	for _, m := range mounts {
		dest := m.ContainerPath
		if dest == "" {
			return nil, nil, nil, fmt.Errorf("mount.ContainerPath is empty")
		}
		if m.GetImage().GetImage() != "" {
			volume, mount, err := s.mountImageVolume(ctx, m, ctr.SandboxConfig().GetMetadata().GetNamespace())
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%w: %w", crierrors.ErrImageVolumeMountFailed, err)
			}
			volumes = append(volumes, *volume)
			ociMounts = append(ociMounts, *mount)
			continue
		}
		if m.HostPath == "" {
			return nil, nil, nil, fmt.Errorf("mount.HostPath is empty")
		}
		if t, ok := devfsMounts[m]; ok {
			if err := devfs.AddRules(specgen.Config.Mounts, t.Rules); err != nil {
				return nil, nil, nil, fmt.Errorf("host path %s: %w", m.HostPath, err)
			}
			if t.Mount {
				specgen.AddMount(devfs.NewMount(dest))
			}
			log.Infof(ctx, "Host path %s is on devfs: %d devfs rules, devfs mount: %v", m.HostPath, len(t.Rules), t.Mount)
			volumes = append(volumes, oci.ContainerVolume{
				ContainerPath: dest,
				HostPath:      m.HostPath,
				Readonly:      m.Readonly,
				Propagation:   m.Propagation,
			})
			continue
		}
		if m.HostPath == "/" && dest == "/" {
			log.Warnf(ctx, "Configuration specifies mounting host root to the container root.  This is dangerous (especially with privileged containers) and should be avoided.")
		}
		src := filepath.Join(s.config.BindMountPrefix, m.HostPath)

		resolvedSrc, err := resolveSymbolicLink(s.config.BindMountPrefix, src)
		if err == nil {
			src = resolvedSrc
		} else {
			if !os.IsNotExist(err) {
				return nil, nil, nil, fmt.Errorf("failed to resolve symlink %q: %w", src, err)
			}
			for _, toReject := range s.config.AbsentMountSourcesToReject {
				if filepath.Clean(src) == toReject {
					// special-case /etc/hostname, as we don't want it to be created as a directory
					// This can cause issues with node reboot.
					return nil, nil, nil, fmt.Errorf("cannot mount %s: path does not exist and will cause issues as a directory", toReject)
				}
			}
			if !ctr.Restore() {
				// Although this would also be really helpful for restoring containers
				// it is problematic as during restore external bind mounts need to be
				// a file if the destination is a file. Unfortunately it is not easy
				// to tell if the destination is a file or a directory. Especially if
				// the destination is a nested bind mount. For now we will just not
				// create the missing bind mount source for restore and return an
				// error to the user.
				if err = os.MkdirAll(src, 0o755); err != nil {
					return nil, nil, nil, fmt.Errorf("failed to mkdir %s: %s", src, err)
				}
			}
		}

		options := []string{"rw"}
		if m.Readonly {
			options = []string{"ro"}
		}

		// mount propagation
		//
		// Ignore this for the most part but may revisit if/when FreeBSD
		// supports linux-style bind mounts in some way.
		switch m.Propagation {
		case types.MountPropagation_PROPAGATION_PRIVATE:
			options = append(options, "rprivate")
			// Since default root propagation in runc is rprivate ignore
			// setting the root propagation
		case types.MountPropagation_PROPAGATION_BIDIRECTIONAL:
			//if err := ensureShared(src, mountInfos); err != nil {
			//	return nil, nil, err
			//}
			options = append(options, "rshared")
		case types.MountPropagation_PROPAGATION_HOST_TO_CONTAINER:
			//if err := ensureSharedOrSlave(src, mountInfos); err != nil {
			//	return nil, nil, err
			//}
			options = append(options, "rslave")
		default:
			log.Warnf(ctx, "Unknown propagation mode for hostPath %q", m.HostPath)
			options = append(options, "rprivate")
		}

		volumes = append(volumes, oci.ContainerVolume{
			ContainerPath:  dest,
			HostPath:       src,
			Readonly:       m.Readonly,
			Propagation:    m.Propagation,
			SelinuxRelabel: m.SelinuxRelabel,
		})

		ociMounts = append(ociMounts, rspec.Mount{
			Source:      src,
			Destination: dest,
			Options:     options,
		})

		// Linux binds host paths recursively; see hostSubmounts.
		if fi, err := os.Stat(src); err == nil && fi.IsDir() {
			subs := hostSubmounts(src, dest, m.Readonly, s.submountExcludes(), mountSet)
			if len(subs) > 0 {
				log.Debugf(ctx, "Host path %s: also mounting %d file systems below it", src, len(subs))
			}
			ociMounts = append(ociMounts, subs...)
		}
	}

	return volumes, ociMounts, nil, nil
}

// mountImageVolume mounts the image a Kubernetes image volume refers to
// (read-only, from containers/storage) and returns the volume and a read-only
// nullfs mount of it, or of its sub path, for the container's jail. Linux
// uses an overlay with an empty upper directory to the same effect.
func (s *Server) mountImageVolume(ctx context.Context, m *types.Mount, namespace string) (*oci.ContainerVolume, *rspec.Mount, error) {
	ref := m.GetImage().GetImage()
	status, err := s.storageImageStatus(ctx, &types.ImageSpec{Image: ref})
	if err != nil {
		return nil, nil, fmt.Errorf("get storage image status: %w", err)
	}
	if status == nil {
		// The kubelet pulls the image first.
		return nil, nil, fmt.Errorf("image %q does not exist locally", ref)
	}
	if err := s.verifyImageSignature(ctx, namespace, m.GetImage().GetUserSpecifiedImage(), status); err != nil {
		return nil, nil, err
	}
	imageID := status.ID.IDStringForOutOfProcessConsumptionOnly()

	mountPoint, err := s.ContainerServer.Store().MountImage(imageID, []string{"ro"}, "")
	if err != nil {
		return nil, nil, fmt.Errorf("mount storage: %w", err)
	}
	log.Infof(ctx, "Image %s mounted to: %s", ref, mountPoint)

	src := mountPoint
	if sub := m.GetImageSubPath(); sub != "" {
		// The kubelet already rejects absolute paths and "..". Resolve
		// symlinks inside the image, which is read-only.
		src, err = securejoin.SecureJoin(mountPoint, sub)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve sub path %q: %w", sub, err)
		}
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				return nil, nil, fmt.Errorf("sub path %q does not exist in image volume %q", sub, ref)
			}
			return nil, nil, fmt.Errorf("stat sub path %q: %w", sub, err)
		}
	}

	return &oci.ContainerVolume{
		ContainerPath:     m.GetContainerPath(),
		HostPath:          src,
		Readonly:          true,
		RecursiveReadOnly: m.GetRecursiveReadOnly(),
		Propagation:       m.GetPropagation(),
		Image:             &types.ImageSpec{Image: imageID},
	}, &rspec.Mount{
		Source:      src,
		Destination: m.GetContainerPath(),
		Options:     []string{"ro", "nosuid", "noexec"},
	}, nil
}

func addShmMount(ctr ctrfactory.Container, sb *sandbox.Sandbox) {
}

// setupSystemdMounts is a no-op on FreeBSD as systemd is not supported.
func setupSystemdMounts(g *generate.Generator) {
}

func newLinuxContainerSecurityContext() *types.LinuxContainerSecurityContext {
	return &types.LinuxContainerSecurityContext{
		Capabilities:     &types.Capability{},
		NamespaceOptions: &types.NamespaceOption{},
		SelinuxOptions:   &types.SELinuxOption{},
		RunAsUser:        &types.Int64Value{},
		RunAsGroup:       &types.Int64Value{},
		Seccomp:          &types.SecurityProfile{},
		Apparmor:         &types.SecurityProfile{},
	}
}

// Returns the spec Generator for the container, with some values set.
func (s *Server) getSpecGen(ctr ctrfactory.Container, containerConfig *types.ContainerConfig) *generate.Generator {
	specgen := ctr.Spec()
	specgen.HostSpecific = true
	specgen.ClearProcessRlimits()

	for _, u := range s.config.Ulimits() {
		specgen.AddProcessRlimits(u.Name, u.Hard, u.Soft)
	}

	specgen.SetRootReadonly(ctr.ReadOnly(s.config.ReadOnly))

	if s.config.ReadOnly {
		// tmpcopyup is a runc extension and is not part of the OCI spec.
		// WORK ON: Use "overlay" mounts as an alternative to tmpfs with tmpcopyup
		// Look at https://github.com/cri-o/cri-o/pull/1434#discussion_r177200245 for more info on this
		options := []string{"rw", "noexec", "nosuid", "nodev", "tmpcopyup"}
		mounts := map[string]string{
			"/tmp":     "mode=1777",
			"/var/run": "mode=0755",
			"/var/tmp": "mode=1777",
		}
		for target, mode := range mounts {
			if !isInCRIMounts(target, containerConfig.Mounts) {
				ctr.SpecAddMount(rspec.Mount{
					Destination: target,
					Type:        "tmpfs",
					Source:      "tmpfs",
					Options:     append(options, mode),
				})
			}
		}
	}

	return specgen
}

func (s *Server) specSetApparmorProfile(ctx context.Context, specgen *generate.Generator, ctr ctrfactory.Container, securityContext *types.LinuxContainerSecurityContext) error {
	return nil
}

func (s *Server) specSetBlockioClass(specgen *generate.Generator, containerName string, containerAnnotations, sandboxAnnotations map[string]string) error {
	return nil
}

func (s *Server) specSetDevices(ctr ctrfactory.Container, sb *sandbox.Sandbox) error {
	configuredDevices := s.config.Devices()

	privilegedWithoutHostDevices, err := s.Runtime().PrivilegedWithoutHostDevices(sb.RuntimeHandler())
	if err != nil {
		return err
	}

	devicesAnnotationValue, _ := v2.GetAnnotationValue(sb.Annotations(), v2.Devices)
	annotationDevices, err := device.DevicesFromAnnotation(devicesAnnotationValue, s.config.AllowedDevices)
	if err != nil {
		return err
	}

	if err := ctr.SpecAddDevices(configuredDevices, annotationDevices, privilegedWithoutHostDevices, s.config.DeviceOwnershipFromSecurityContext); err != nil {
		return err
	}
	// CAP_NET_RAW on Linux lets a process read packets (AF_PACKET), as tools
	// like tcpdump and arping need. The jail equivalent is the bpf device,
	// which the default devfs ruleset hides; a vnet jail's bpf only sees the
	// pod's own interfaces. Raw IP sockets are already allowed for all pods
	// (allow.raw_sockets), as Linux's default capabilities don't allow them.
	caps := ctr.Config().GetLinux().GetSecurityContext().GetCapabilities()
	if !ctr.Privileged() && hasCapability(caps, s.config.DefaultCapabilities, "NET_RAW") {
		for i, m := range ctr.Spec().Config.Mounts {
			if m.Type == "devfs" && filepath.Clean(m.Destination) == "/dev" {
				ctr.Spec().Config.Mounts[i].Options = append(m.Options, "rule=path bpf* unhide")
			}
		}
	}
	// CAP_MKNOD lets a Linux container create device nodes; allow.mknod
	// lets a jail create them outside devfs, where they are inert.
	if jailparam.MknodAllowed(ctr.Privileged(), hasCapability(caps, s.config.DefaultCapabilities, "MKNOD")) {
		ctr.Spec().AddAnnotation(jailparam.AllowMknodAnnotation, "true")
	}
	return nil
}

// hasCapability reports whether the container ends up with the capability
// name (e.g. "NET_RAW"), the way SpecSetupCapabilities computes it: the
// defaults, plus the capabilities the container adds, minus those it drops.
func hasCapability(caps *types.Capability, defaults []string, name string) bool {
	has := func(list []string, name string) bool {
		for _, c := range list {
			if strings.EqualFold(strings.TrimPrefix(strings.ToUpper(c), "CAP_"), name) {
				return true
			}
		}
		return false
	}
	if has(caps.GetDropCapabilities(), name) {
		return false
	}
	if has(caps.GetAddCapabilities(), name) || has(caps.GetAddCapabilities(), "ALL") {
		return true
	}
	if has(caps.GetDropCapabilities(), "ALL") {
		return false
	}
	return has(defaults, name)
}

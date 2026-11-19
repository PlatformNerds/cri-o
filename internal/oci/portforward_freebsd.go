package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"go.podman.io/storage/pkg/reexec"
	"golang.org/x/sys/unix"

	"github.com/cri-o/cri-o/internal/log"
)

// Port forwarding on FreeBSD. A pod's network stack is its infra container's
// jail (a vnet jail, or the host's for hostNetwork pods), and a process can
// only open sockets in it by attaching to the jail, which jail_attach(2) does
// for the whole process, so it can't be done from a goroutine like Linux's
// setns(2). A short-lived child (crio re-executed as "crio-portforward")
// attaches to the jail, connects to localhost:port there and hands the
// connected socket back over a socketpair (SCM_RIGHTS); crio then copies the
// streams as on Linux.
//
// Without this, the generic Runtime.PortForwardContainer called itself (the
// FreeBSD runtimeOCI had no PortForwardContainer of its own) until the stack
// overflowed, and every `kubectl port-forward` crashed crio.

const portForwardReexec = "crio-portforward"

func init() {
	reexec.Register(portForwardReexec, portForwardChild)
}

// PortForwardContainer forwards the specified port into the provided container.
func (r *runtimeOCI) PortForwardContainer(ctx context.Context, c *Container, netNsPath string, port int32, stream io.ReadWriteCloser) error {
	ctx, span := log.StartSpan(ctx)
	defer span.End()
	defer stream.Close()

	var conn net.Conn
	var err error
	if c.Spoofed() {
		// No infra jail: a hostNetwork pod (drop_infra_ctr), whose
		// network stack is the host's own.
		log.Infof(ctx, "Starting port forward for %s on the host network", c.ID())
		conn, err = dialLocalhost(port)
	} else {
		log.Infof(ctx, "Starting port forward for %s in jail %s", c.ID(), c.ID())
		conn, err = dialInJail(ctx, c.ID(), port)
	}
	if err != nil {
		return fmt.Errorf("port forward into jail %s: %w", c.ID(), err)
	}
	defer conn.Close()

	debug := func(format string, args ...any) {
		log.Debugf(ctx, fmt.Sprintf("PortForward (id: %s, port: %d): %s", c.ID(), port, format), args...)
	}

	errCh := make(chan error, 2)
	go func() {
		debug("copy data from container to client")
		_, err := io.Copy(stream, conn)
		errCh <- err
	}()
	go func() {
		debug("copy data from client to container")
		_, err := io.Copy(conn, stream)
		// Let the container see EOF while the other direction drains.
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		errCh <- err
	}()

	var errFwd error
	select {
	case errFwd = <-errCh:
		debug("stop forwarding in direction: %v", errFwd)
	case <-ctx.Done():
		debug("cancelled: %v", ctx.Err())
		return ctx.Err()
	}

	// give a chance to terminate gracefully or timeout
	select {
	case e := <-errCh:
		if errFwd == nil {
			errFwd = e
		}
		debug("stopped forwarding in both directions")
	case <-time.After(time.Second):
		debug("timed out waiting to close the connection")
	case <-ctx.Done():
		debug("cancelled: %v", ctx.Err())
		errFwd = ctx.Err()
	}

	if errFwd != nil {
		return fmt.Errorf("port forward into jail %s: %w", c.ID(), errFwd)
	}
	log.Infof(ctx, "Finished port forwarding for %q on port %d", c.ID(), port)
	return nil
}

// dialInJail connects to localhost:port inside the named jail's network stack.
func dialInJail(ctx context.Context, jail string, port int32) (net.Conn, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("socketpair: %w", err)
	}
	parent := os.NewFile(uintptr(fds[0]), "portforward-parent")
	child := os.NewFile(uintptr(fds[1]), "portforward-child")
	defer parent.Close()

	var stderr strings.Builder
	cmd := reexec.CommandContext(ctx, portForwardReexec, jail, strconv.Itoa(int(port)))
	cmd.ExtraFiles = []*os.File{child} // fd 3 in the child
	cmd.Stderr = &stderr
	err = cmd.Run()
	child.Close()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}

	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4))
	_, oobn, _, _, err := unix.Recvmsg(int(parent.Fd()), buf, oob, 0)
	if err != nil {
		return nil, fmt.Errorf("receiving the socket: %w", err)
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) != 1 {
		return nil, fmt.Errorf("receiving the socket: no descriptor (%v)", err)
	}
	rights, err := unix.ParseUnixRights(&msgs[0])
	if err != nil || len(rights) != 1 {
		return nil, fmt.Errorf("receiving the socket: no descriptor (%v)", err)
	}
	f := os.NewFile(uintptr(rights[0]), "portforward-conn")
	defer f.Close()
	return net.FileConn(f)
}

// portForwardChild runs as "crio-portforward <jail> <port>" with the
// socketpair on fd 3: it attaches to the jail, connects to localhost:port,
// sends the connected socket back and exits. Errors go to stderr.
func portForwardChild() {
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		os.Exit(1)
	}
	if len(os.Args) != 3 {
		fail("usage: %s <jail> <port>", portForwardReexec)
	}
	port, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fail("bad port %q", os.Args[2])
	}
	jid, err := jailID(os.Args[1])
	if err != nil {
		fail("jail %s: %v", os.Args[1], err)
	}
	if _, _, e := unix.Syscall(unix.SYS_JAIL_ATTACH, uintptr(jid), 0, 0); e != 0 {
		fail("jail_attach %s: %v", os.Args[1], e)
	}

	conn, err := dialLocalhost(int32(port))
	if err != nil {
		fail("inside jail %s: %v", os.Args[1], err)
	}
	f, err := conn.(*net.TCPConn).File()
	if err != nil {
		fail("%v", err)
	}
	if err := unix.Sendmsg(3, []byte{0}, unix.UnixRights(int(f.Fd())), nil, 0); err != nil {
		fail("sending the socket: %v", err)
	}
	os.Exit(0)
}

// dialLocalhost connects to localhost:port. localhost may mean IPv4 or IPv6
// and the server may listen on only one: try both, one after the other (as
// Linux's dialer with FallbackDelay -1).
func dialLocalhost(port int32) (net.Conn, error) {
	var errs []error
	for _, addr := range []string{"127.0.0.1", "::1"} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(addr, strconv.Itoa(int(port))), 10*time.Second)
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("failed to connect to localhost:%d: %w", port, errors.Join(errs...))
}

// jailID returns the jid of the jail with the given name, from jail_get(2).
func jailID(name string) (int32, error) {
	var jid int32
	iov := func(b []byte) unix.Iovec {
		v := unix.Iovec{Base: &b[0]}
		v.SetLen(len(b))
		return v
	}
	nameKey := []byte("name\x00")
	nameVal := append([]byte(name), 0)
	jidKey := []byte("jid\x00")
	jidVal := (*[4]byte)(unsafe.Pointer(&jid))[:]
	iovs := []unix.Iovec{iov(nameKey), iov(nameVal), iov(jidKey), iov(jidVal)}
	r, _, e := unix.Syscall(unix.SYS_JAIL_GET, uintptr(unsafe.Pointer(&iovs[0])), uintptr(len(iovs)), 0)
	if e != 0 {
		return 0, e
	}
	return int32(r), nil
}

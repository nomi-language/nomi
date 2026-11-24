package stdhttp

import (
	"crypto/tls"
	"errors"
	"math"
	"net"
	"runtime"
	"strconv"
	"syscall"

	"github.com/nomi-language/nomi/rt"
)

// Socket options: what std/net's and std/udp's setters set on a socket
// they hold, and what a listener or a bound socket sets before it binds.
// Each option is a sockopt the system's file names (sockopt_unix.go,
// sockopt_windows.go, sockopt_none.go), set and read through the socket's
// syscall.RawConn, so TCP, Unix and datagram sockets share one path.

// sockopt is a socket option as the system numbers it: its level and
// name. The zero sockopt is one this system lacks.
type sockopt struct{ level, name int }

// unsupported is a failure because this system, or this kind of socket,
// lacks what was asked: net.Error's Unsupported.
type unsupported string

func (u unsupported) Error() string { return string(u) }

// socketOption is an option a std function sets by name: the option on an
// IPv4 socket and on an IPv6 one, and whether a Unix socket has it, which
// only a socket-level option does (its ip4 and ip6 are then the same).
type socketOption struct {
	ip4, ip6 sockopt
	unix     bool
}

// socketOptions are the options `std::http::host.socket_set` and
// `socket_get` take, by the name std/net and std/udp pass.
var socketOptions = map[string]socketOption{
	"broadcast":          {soBroadcast, soBroadcast, false},
	"read_buffer":        {soRcvbuf, soRcvbuf, true},
	"write_buffer":       {soSndbuf, soSndbuf, true},
	"ttl":                {ipTTL, ipv6UnicastHops, false},
	"multicast_ttl":      {ipMulticastTTL, ipv6MulticastHops, false},
	"multicast_loopback": {ipMulticastLoop, ipv6MulticastLoop, false},
}

// socketOf is the connection under c's TLS, or c's own when it is not TLS.
func socketOf(c rt.NetConn) net.Conn {
	inner := connOf(c).c
	if t, ok := inner.(*tls.Conn); ok {
		return t.NetConn()
	}
	return inner
}

// rawOf is the socket c holds and its family: 4 or 6 by the address it is
// bound to, or 0 for a Unix socket. Where there are no sockets, no
// connection is one.
func rawOf(c rt.NetConn) (syscall.RawConn, int, error) {
	if !hasSockets {
		return nil, 0, unsupported("the connection is not a socket")
	}
	inner := socketOf(c)
	sc, ok := inner.(syscall.Conn)
	if !ok {
		return nil, 0, unsupported("the connection is not a socket")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return nil, 0, err
	}
	family := 0
	switch a := inner.LocalAddr().(type) {
	case *net.TCPAddr:
		family = ipFamily(a.IP)
	case *net.UDPAddr:
		family = ipFamily(a.IP)
	}
	return raw, family, nil
}

func ipFamily(ip net.IP) int {
	if ip.To4() != nil {
		return 4
	}
	return 6
}

// onSocket runs f on c's descriptor with the option name names for c's
// family.
func onSocket(c rt.NetConn, name string, f func(fd uintptr, o sockopt) error) error {
	opt, ok := socketOptions[name]
	if !ok {
		rt.Trap("net: no socket option " + name)
	}
	raw, family, err := rawOf(c)
	if err != nil {
		return err
	}
	o := opt.ip4
	switch {
	case family == 0 && !opt.unix:
		return unsupported("a Unix socket has no " + name + " option")
	case family == 6:
		o = opt.ip6
	}
	if o == (sockopt{}) {
		return unsupported(runtime.GOOS + " has no " + name + " option")
	}
	var ferr error
	if err := raw.Control(func(fd uintptr) { ferr = f(fd, o) }); err != nil {
		return err
	}
	return ferr
}

// SocketSet is `std::http::host.socket_set`: sets the option `name`
// (socketOptions) to value, a Bool as 0 or 1.
func SocketSet(c rt.NetConn, name string, valueArg rt.Int) rt.Result[rt.Unit, *rt.List[string]] {
	v := int(max(min(valueArg.Saturated(), math.MaxInt32), math.MinInt32))
	if err := onSocket(c, name, func(fd uintptr, o sockopt) error { return sysSetInt(fd, o, v) }); err != nil {
		return rt.Err[rt.Unit](netErr("set_"+name, socketAddress(c), err))
	}
	return rt.Ok[rt.Unit, *rt.List[string]](rt.Unit{})
}

// SocketGet is `std::http::host.socket_get`: the option `name`'s value, a
// Bool as 0 for off and any other number for on.
func SocketGet(c rt.NetConn, name string) rt.Result[rt.Int, *rt.List[string]] {
	var v int
	if err := onSocket(c, name, func(fd uintptr, o sockopt) (err error) {
		v, err = sysGetInt(fd, o)
		return err
	}); err != nil {
		return rt.Err[rt.Int](netErr(name, socketAddress(c), err))
	}
	return rt.Ok[rt.Int, *rt.List[string]](rt.SmallInt(int64(v)))
}

// SocketGroup is `std::http::host.socket_group`: joins the multicast
// group, or leaves it, on the interface named (by an IPv4 address of it
// for an IPv4 group, by its index for an IPv6 one), or on the one the
// system picks for "".
func SocketGroup(c rt.NetConn, join bool, group, ifname string) rt.Result[rt.Unit, *rt.List[string]] {
	op := "join_group"
	if !join {
		op = "leave_group"
	}
	fail := func(err error) rt.Result[rt.Unit, *rt.List[string]] { return rt.Err[rt.Unit](netErr(op, group, err)) }
	if !hasSockets {
		return fail(errors.New(noSocketsMessage))
	}
	ip := net.ParseIP(group)
	if ip == nil || !ip.IsMulticast() {
		return rt.Err[rt.Unit](listOf([]string{"InvalidAddress", group, "not a multicast address"}))
	}
	ip4 := ip.To4()
	err := onMulticast(c, ifname, ip4 != nil, func(fd uintptr, local [4]byte) error {
		o := ipAddMembership
		if !join {
			o = ipDropMembership
		}
		return sysSetMreq4(fd, o, [4]byte(ip4), local)
	}, func(fd uintptr, index int) error {
		o := ipv6JoinGroup
		if !join {
			o = ipv6LeaveGroup
		}
		return sysSetMreq6(fd, o, [16]byte(ip.To16()), uint32(index))
	})
	if err != nil {
		return fail(err)
	}
	return rt.Ok[rt.Unit, *rt.List[string]](rt.Unit{})
}

// SocketMulticastInterface is `std::http::host.socket_multicast_interface`:
// the socket's multicast datagrams go out of the interface named, or the
// one the system picks for "" (IP_MULTICAST_IF, IPV6_MULTICAST_IF).
func SocketMulticastInterface(c rt.NetConn, ifname string) rt.Result[rt.Unit, *rt.List[string]] {
	_, family, err := rawOf(c)
	if hasSockets && err == nil {
		err = onMulticast(c, ifname, family == 4, func(fd uintptr, local [4]byte) error {
			return sysSetInet4(fd, ipMulticastIf, local)
		}, func(fd uintptr, index int) error {
			return sysSetInt(fd, ipv6MulticastIf, index)
		})
	}
	if err != nil {
		return rt.Err[rt.Unit](netErr("set_multicast_interface", socketAddress(c), err))
	}
	return rt.Ok[rt.Unit, *rt.List[string]](rt.Unit{})
}

// onMulticast runs a multicast option on c's descriptor with the interface
// named ifname, "" for the one the system picks: ip4 on an IPv4 option,
// given the interface's first IPv4 address, which names it there, and ip6
// on an IPv6 one, given its index.
func onMulticast(c rt.NetConn, ifname string, v4 bool, ip4 func(fd uintptr, local [4]byte) error, ip6 func(fd uintptr, index int) error) error {
	var ifi *net.Interface
	if ifname != "" {
		var err error
		if ifi, err = net.InterfaceByName(ifname); err != nil {
			return err
		}
	}
	raw, family, err := rawOf(c)
	if err != nil {
		return err
	}
	if family == 0 {
		return unsupported("a Unix socket has no multicast")
	}
	var serr error
	err = raw.Control(func(fd uintptr) {
		if v4 {
			var local [4]byte
			if ifi != nil {
				if local, serr = interfaceIPv4(ifi); serr != nil {
					return
				}
			}
			serr = ip4(fd, local)
			return
		}
		index := 0
		if ifi != nil {
			index = ifi.Index
		}
		serr = ip6(fd, index)
	})
	if err != nil {
		return err
	}
	return serr
}

// interfaceIPv4 is the first IPv4 address of ifi, which names it to an
// IPv4 multicast option.
func interfaceIPv4(ifi *net.Interface) ([4]byte, error) {
	addrs, err := ifi.Addrs()
	if err != nil {
		return [4]byte{}, err
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if ip4 := n.IP.To4(); ip4 != nil {
				return [4]byte(ip4), nil
			}
		}
	}
	return [4]byte{}, errors.New("the interface " + ifi.Name + " has no IPv4 address")
}

// bindControl is the Control of a listener, a dialer or a datagram socket
// that sets SO_REUSEADDR and SO_REUSEPORT before it binds, or nil for
// neither.
func bindControl(reuseAddress, reusePort bool) func(network, address string, c syscall.RawConn) error {
	if !reuseAddress && !reusePort {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var err error
		cerr := c.Control(func(fd uintptr) {
			for _, o := range []struct {
				on   bool
				opt  sockopt
				what string
			}{{reuseAddress, soReuseAddr, "SO_REUSEADDR"}, {reusePort, soReusePort, "SO_REUSEPORT"}} {
				switch {
				case !o.on || err != nil:
				case o.opt == (sockopt{}):
					err = unsupported(runtime.GOOS + " has no " + o.what)
				default:
					err = sysSetInt(fd, o.opt, 1)
				}
			}
		})
		if cerr != nil {
			return cerr
		}
		return err
	}
}

// socketAddress names c's socket in an error: its own address, or its
// peer's when it is bound to none, as a dialed Unix socket is.
func socketAddress(c rt.NetConn) string {
	inner := socketOf(c)
	if a := addrString(inner.LocalAddr()); a != "" {
		return a
	}
	return addrString(inner.RemoteAddr())
}

// addrString is a's text, "" for none: an unbound Unix socket's peer.
func addrString(a net.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
}

// addrParts is a datagram socket's address as std/udp's Address reads it:
// ["Ip", host, port] or ["Unix", path], or none.
func addrParts(a net.Addr) []string {
	switch a := a.(type) {
	case *net.UDPAddr:
		if a == nil {
			return nil
		}
		ap := a.AddrPort()
		return []string{"Ip", ap.Addr().Unmap().String(), strconv.Itoa(int(ap.Port()))}
	case *net.UnixAddr:
		if a == nil {
			return []string{"Unix", ""}
		}
		return []string{"Unix", a.Name}
	}
	return nil
}

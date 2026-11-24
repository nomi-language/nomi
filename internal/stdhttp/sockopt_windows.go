//go:build windows

package stdhttp

import "golang.org/x/sys/windows"

// The socket options as Windows numbers them. Windows has no SO_REUSEPORT:
// a listener that asks for it fails with net.Error's Unsupported.
var (
	soBroadcast       = sockopt{windows.SOL_SOCKET, windows.SO_BROADCAST}
	soReuseAddr       = sockopt{windows.SOL_SOCKET, windows.SO_REUSEADDR}
	soReusePort       = sockopt{}
	soRcvbuf          = sockopt{windows.SOL_SOCKET, windows.SO_RCVBUF}
	soSndbuf          = sockopt{windows.SOL_SOCKET, windows.SO_SNDBUF}
	ipTTL             = sockopt{windows.IPPROTO_IP, windows.IP_TTL}
	ipMulticastTTL    = sockopt{windows.IPPROTO_IP, windows.IP_MULTICAST_TTL}
	ipMulticastLoop   = sockopt{windows.IPPROTO_IP, windows.IP_MULTICAST_LOOP}
	ipMulticastIf     = sockopt{windows.IPPROTO_IP, windows.IP_MULTICAST_IF}
	ipAddMembership   = sockopt{windows.IPPROTO_IP, windows.IP_ADD_MEMBERSHIP}
	ipDropMembership  = sockopt{windows.IPPROTO_IP, windows.IP_DROP_MEMBERSHIP}
	ipv6UnicastHops   = sockopt{windows.IPPROTO_IPV6, windows.IPV6_UNICAST_HOPS}
	ipv6MulticastHops = sockopt{windows.IPPROTO_IPV6, windows.IPV6_MULTICAST_HOPS}
	ipv6MulticastLoop = sockopt{windows.IPPROTO_IPV6, windows.IPV6_MULTICAST_LOOP}
	ipv6MulticastIf   = sockopt{windows.IPPROTO_IPV6, windows.IPV6_MULTICAST_IF}
	ipv6JoinGroup     = sockopt{windows.IPPROTO_IPV6, windows.IPV6_JOIN_GROUP}
	ipv6LeaveGroup    = sockopt{windows.IPPROTO_IPV6, windows.IPV6_LEAVE_GROUP}
)

func sysSetInt(fd uintptr, o sockopt, v int) error {
	return windows.SetsockoptInt(windows.Handle(fd), o.level, o.name, v)
}

func sysGetInt(fd uintptr, o sockopt) (int, error) {
	return windows.GetsockoptInt(windows.Handle(fd), o.level, o.name)
}

func sysSetInet4(fd uintptr, o sockopt, v [4]byte) error {
	return windows.SetsockoptInet4Addr(windows.Handle(fd), o.level, o.name, v)
}

func sysSetMreq4(fd uintptr, o sockopt, group, local [4]byte) error {
	return windows.SetsockoptIPMreq(windows.Handle(fd), o.level, o.name, &windows.IPMreq{Multiaddr: group, Interface: local})
}

func sysSetMreq6(fd uintptr, o sockopt, group [16]byte, index uint32) error {
	return windows.SetsockoptIPv6Mreq(windows.Handle(fd), o.level, o.name, &windows.IPv6Mreq{Multiaddr: group, Interface: index})
}

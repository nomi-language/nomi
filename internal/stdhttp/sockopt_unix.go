//go:build unix && !solaris

package stdhttp

import "golang.org/x/sys/unix"

// The socket options as a Unix system numbers them. Solaris and illumos,
// which name no SO_REUSEPORT, take sockopt_none.go.
var (
	soBroadcast       = sockopt{unix.SOL_SOCKET, unix.SO_BROADCAST}
	soReuseAddr       = sockopt{unix.SOL_SOCKET, unix.SO_REUSEADDR}
	soReusePort       = sockopt{unix.SOL_SOCKET, unix.SO_REUSEPORT}
	soRcvbuf          = sockopt{unix.SOL_SOCKET, unix.SO_RCVBUF}
	soSndbuf          = sockopt{unix.SOL_SOCKET, unix.SO_SNDBUF}
	ipTTL             = sockopt{unix.IPPROTO_IP, unix.IP_TTL}
	ipMulticastTTL    = sockopt{unix.IPPROTO_IP, unix.IP_MULTICAST_TTL}
	ipMulticastLoop   = sockopt{unix.IPPROTO_IP, unix.IP_MULTICAST_LOOP}
	ipMulticastIf     = sockopt{unix.IPPROTO_IP, unix.IP_MULTICAST_IF}
	ipAddMembership   = sockopt{unix.IPPROTO_IP, unix.IP_ADD_MEMBERSHIP}
	ipDropMembership  = sockopt{unix.IPPROTO_IP, unix.IP_DROP_MEMBERSHIP}
	ipv6UnicastHops   = sockopt{unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS}
	ipv6MulticastHops = sockopt{unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_HOPS}
	ipv6MulticastLoop = sockopt{unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_LOOP}
	ipv6MulticastIf   = sockopt{unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF}
	ipv6JoinGroup     = sockopt{unix.IPPROTO_IPV6, unix.IPV6_JOIN_GROUP}
	ipv6LeaveGroup    = sockopt{unix.IPPROTO_IPV6, unix.IPV6_LEAVE_GROUP}
)

func sysSetInt(fd uintptr, o sockopt, v int) error {
	return unix.SetsockoptInt(int(fd), o.level, o.name, v)
}

func sysGetInt(fd uintptr, o sockopt) (int, error) {
	return unix.GetsockoptInt(int(fd), o.level, o.name)
}

func sysSetInet4(fd uintptr, o sockopt, v [4]byte) error {
	return unix.SetsockoptInet4Addr(int(fd), o.level, o.name, v)
}

func sysSetMreq4(fd uintptr, o sockopt, group, local [4]byte) error {
	return unix.SetsockoptIPMreq(int(fd), o.level, o.name, &unix.IPMreq{Multiaddr: group, Interface: local})
}

func sysSetMreq6(fd uintptr, o sockopt, group [16]byte, index uint32) error {
	return unix.SetsockoptIPv6Mreq(int(fd), o.level, o.name, &unix.IPv6Mreq{Multiaddr: group, Interface: index})
}

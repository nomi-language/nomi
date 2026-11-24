//go:build !(unix && !solaris) && !windows

package stdhttp

// A system with no socket options std sets: js/wasm and wasip1, which have
// no sockets, and Solaris and illumos. Every option is the zero sockopt,
// so each setter fails with net.Error's Unsupported before it reaches
// these.
var (
	soBroadcast, soReuseAddr, soReusePort, soRcvbuf, soSndbuf sockopt
	ipTTL, ipMulticastTTL, ipMulticastLoop, ipMulticastIf     sockopt
	ipAddMembership, ipDropMembership                         sockopt
	ipv6UnicastHops, ipv6MulticastHops, ipv6MulticastLoop     sockopt
	ipv6MulticastIf                                           sockopt
	ipv6JoinGroup, ipv6LeaveGroup                             sockopt
)

func sysSetInt(uintptr, sockopt, int) error { return unsupported("this system sets no socket options") }
func sysGetInt(uintptr, sockopt) (int, error) {
	return 0, unsupported("this system reads no socket options")
}
func sysSetInet4(uintptr, sockopt, [4]byte) error {
	return unsupported("this system sets no socket options")
}
func sysSetMreq4(uintptr, sockopt, [4]byte, [4]byte) error {
	return unsupported("this system has no multicast groups")
}
func sysSetMreq6(uintptr, sockopt, [16]byte, uint32) error {
	return unsupported("this system has no multicast groups")
}

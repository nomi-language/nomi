package stdhttp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/nomi-language/nomi/internal/osreach"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/rt"
)

// std/udp's Go side: datagram sockets, UDP and Unix, held in the same
// `Conn` as a stream connection (netconn.go), so a socket's deadlines,
// close, addresses and options are the connection's crossings. A bound
// socket receives, as a listener accepts: it is tied to no task's context,
// and the program's drain closes it. A dialed one is tied to the dialing
// task's context, as a dialed connection is. A receive stops when the task
// waiting in it is stopped.

// maxDatagram is the most bytes a receive asks for, whatever `max` says:
// no datagram a system sends is longer.
const maxDatagram = 1 << 26

// UDPBind is `std::http::host.udp_bind`: a datagram socket on network
// ("udp", or "unixgram" for a Unix socket's path) bound to address, with
// SO_REUSEADDR and SO_REUSEPORT set before it binds when asked. A bound
// Unix socket removes its file when it closes, as a Unix listener does.
func UDPBind(fr *rt.Frame, network, address string, reuseAddress, reusePort bool) rt.Result[rt.NetConn, *rt.List[string]] {
	return udpOpen(fr, network, address, true, reuseAddress, reusePort)
}

// UDPDial is `std::http::host.udp_dial`: a datagram socket on network
// connected to address, which sends to and receives from it alone.
func UDPDial(fr *rt.Frame, network, address string) rt.Result[rt.NetConn, *rt.List[string]] {
	return udpOpen(fr, network, address, false, false, false)
}

func udpOpen(fr *rt.Frame, network, address string, bind, reuseAddress, reusePort bool) rt.Result[rt.NetConn, *rt.List[string]] {
	op := "dial"
	if bind {
		op = "bind"
	}
	fail := func(err error) rt.Result[rt.NetConn, *rt.List[string]] {
		return rt.Err[rt.NetConn](netErr(op, address, err))
	}
	if !hasSockets {
		return fail(errors.New(noSocketsMessage))
	}
	if network == "unixgram" && runtime.GOOS == "windows" {
		return fail(unsupported("Windows has no Unix datagram sockets"))
	}
	network = ipNetwork(network, address)
	ctx := frameContext(fr)
	if !bind {
		d, failed := dialer(network, 0, 0, "", reuseAddress, reusePort)
		if failed != nil {
			return rt.Err[rt.NetConn](listOf(failed))
		}
		c, err := d.DialContext(ctx, network, address)
		if err != nil {
			return fail(err)
		}
		return rt.Ok[rt.NetConn, *rt.List[string]](newConn(ctx, c))
	}
	osreach.Note("listen")
	lc := &net.ListenConfig{Control: bindControl(reuseAddress, reusePort)}
	pc, err := lc.ListenPacket(ctx, network, address)
	if err != nil {
		return fail(err)
	}
	file := ""
	if network == "unixgram" && address != "" && address[0] != '@' {
		file = address
	}
	return rt.Ok[rt.NetConn, *rt.List[string]](rt.NewNetConn(boundSocket(pc.(net.Conn), file)))
}

// boundSocket wraps a bound datagram socket, as a listener is held: tied
// to no task, and closed by the program's drain. Closing it removes the
// Unix socket file at file, "" for none.
func boundSocket(c net.Conn, file string) *conn {
	x := connWith(context.Background(), c)
	var once sync.Once
	closeSocket := func() {
		once.Do(func() {
			_ = c.Close()
			if file != "" {
				_ = os.Remove(file)
			}
		})
	}
	unhook := rt.OnDrain(closeSocket)
	x.release = func() {
		unhook()
		closeSocket()
	}
	return x
}

// ipNetwork is "udp4" or "udp6" for a "udp" address whose host is an IP
// address of that family, so `0.0.0.0:p` binds an IPv4 socket and its
// options are IPv4's, where Go's "udp" would bind both families; network
// itself otherwise.
func ipNetwork(network, address string) string {
	if network != "udp" {
		return network
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return network
	}
	ip, err := netip.ParseAddr(host)
	switch {
	case err != nil:
		return network
	case ip.Is4():
		return "udp4"
	}
	return "udp6"
}

// datagramOf is the datagram socket c holds.
func datagramOf(c rt.NetConn) (net.PacketConn, bool) {
	pc, ok := connOf(c).c.(net.PacketConn)
	return pc, ok
}

// UDPReceive is `std::http::host.udp_receive`: the next datagram, at most
// max bytes, with True and its sender's parts (addrParts); or empty bytes,
// False and a net.Error's parts. A longer datagram is the error Truncated,
// and its bytes are dropped. A receive whose task is stopped is Closed, as
// an accept is: a past read deadline wakes it, and the socket then gets
// back the deadline the program set, so the receives of other tasks,
// which the same deadline woke, wait again.
func UDPReceive(fr *rt.Frame, c rt.NetConn, maxArg rt.Int) (rt.Bytes, bool, *rt.List[string]) {
	if !hasSockets {
		return "", false, netErr("receive", "", errors.New(noSocketsMessage))
	}
	x := connOf(c)
	local := addrString(x.c.LocalAddr())
	pc, ok := datagramOf(c)
	if !ok {
		return "", false, netErr("receive", local, unsupported("the connection is not a datagram socket"))
	}
	limit := min(max(maxArg.Saturated(), 0), maxDatagram)
	buf := make([]byte, limit+1)
	ctx := frameContext(fr)
	for {
		woken := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			_ = x.c.SetReadDeadline(time.Unix(1, 0))
			close(woken)
		})
		n, from, err := pc.ReadFrom(buf)
		if !stop() {
			<-woken
			x.mu.Lock()
			_ = x.c.SetReadDeadline(x.readDeadline)
			x.mu.Unlock()
			if err != nil {
				return "", false, listOf([]string{"Closed"})
			}
		}
		if err != nil && errors.Is(err, os.ErrDeadlineExceeded) && !x.readDeadlinePassed() {
			continue // another task's stop woke this receive
		}
		sender := addrParts(from)
		if _, unix := x.c.(*net.UnixConn); unix && sender == nil {
			// A Unix sender bound to no path.
			sender = []string{"Unix", ""}
		}
		switch {
		case err != nil && stdio.Errno(err) == syscall.EMSGSIZE, err == nil && int64(n) > limit:
			return "", false, listOf([]string{"Truncated", addrString(from), strconv.FormatInt(limit, 10)})
		case err != nil:
			return "", false, netErr("receive", local, err)
		}
		return rt.Bytes(buf[:n]), true, listOf(sender)
	}
}

// readDeadlinePassed reports whether the read deadline the program set has
// passed.
func (x *conn) readDeadlinePassed() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return !x.readDeadline.IsZero() && !time.Now().Before(x.readDeadline)
}

// UDPSend is `std::http::host.udp_send`: data as one datagram, to the
// socket's peer when address is "" and unix False, else to address, a Unix
// socket's path when unix is set and host:port otherwise.
func UDPSend(c rt.NetConn, data rt.Bytes, unix bool, address string) rt.Result[rt.Unit, *rt.List[string]] {
	if !hasSockets {
		return rt.Err[rt.Unit](netErr("send", address, errors.New(noSocketsMessage)))
	}
	x := connOf(c)
	pc, ok := datagramOf(c)
	if !ok {
		return rt.Err[rt.Unit](netErr("send", address, unsupported("the connection is not a datagram socket")))
	}
	var err error
	if address == "" && !unix {
		_, err = x.c.Write([]byte(data))
		address = addrString(x.c.RemoteAddr())
	} else {
		if _, isUnix := x.c.(*net.UnixConn); unix != isUnix {
			return rt.Err[rt.Unit](listOf([]string{"InvalidAddress", address, "a socket sends only to an address of its own family"}))
		}
		network := "udp"
		if unix {
			network = "unixgram"
		}
		var to net.Addr
		if to, err = resolveAddr(network, address); err == nil {
			_, err = pc.WriteTo([]byte(data), to)
		}
	}
	if err != nil {
		return rt.Err[rt.Unit](netErr("send", address, err))
	}
	return rt.Ok[rt.Unit, *rt.List[string]](rt.Unit{})
}

// UDPAddress is `std::http::host.udp_address`: the socket's own address,
// or its peer's when remote is set, as addrParts lists it; none for an
// unconnected socket's peer.
func UDPAddress(c rt.NetConn, remote bool) *rt.List[string] {
	if !hasSockets {
		return listOf(nil)
	}
	x := connOf(c).c
	if remote {
		return listOf(addrParts(x.RemoteAddr()))
	}
	return listOf(addrParts(x.LocalAddr()))
}

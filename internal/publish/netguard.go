package publish

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// Guard stops the server being used to reach its own network. The address of someone's hosting is typed in by a
// customer, so it could just as well be a machine inside the server's network, or the cloud provider's metadata service.
// Every address the program connects to (the hosting itself, and any a server tells it to use for file transfers) is
// looked up here first, and refused if it is not an ordinary public address.
type Guard struct {
	// AllowPrivate lets connections go to private and local addresses. It is for development and tests only.
	AllowPrivate bool
	// Timeout is how long a connection may take to open.
	Timeout time.Duration
}

// ErrBlocked is returned for an address the server may not connect to.
var ErrBlocked = errors.New("that address is not allowed: the hosting must be on the public internet")

var blockedRanges = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
		"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Blocked says whether an IP address is one the server must not connect to.
func Blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range blockedRanges {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Resolve looks a host up and returns the addresses it may be reached at. A name that points at even one blocked address
// is refused outright (a name that mixes public and private addresses is how this kind of check is usually dodged).
func (g Guard) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("the address %q could not be found", host)
		}
		addrs = ips
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("the address %q could not be found", host)
	}
	if !g.AllowPrivate {
		for _, a := range addrs {
			if Blocked(a) {
				return nil, ErrBlocked
			}
		}
	}
	return addrs, nil
}

// DialContext opens a connection to a checked address. The address that was checked is the one connected to (the name is
// not looked up a second time), so it cannot change between the check and the connection.
func (g Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, errors.New("the port must be between 1 and 65535")
	}
	addrs, err := g.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	var last error
	for _, a := range addrs {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

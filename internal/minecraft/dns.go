package minecraft

import (
	"context"
	"errors"
	"net"
	"strings"
)

// Resolver is the part of *net.Resolver used to find the server.
type Resolver interface {
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// maxSRVDepth bounds SRV records that point at names with SRV records of
// their own. node-gamedig follows them without a limit.
const maxSRVDepth = 8

// resolve finds the address to query like node-gamedig's DnsResolver with
// the "_minecraft._tcp" SRV prefix: IP literals are used as they are;
// otherwise the first SRV record's target is resolved (recursively, and
// falling back to the name itself on any failure or a record pointing at
// itself), then the name's first address is used.
func resolve(ctx context.Context, r Resolver, host string, depth int, debugf func(string, ...any)) (string, error) {
	if net.ParseIP(host) != nil {
		return host, nil
	}
	if depth < maxSRVDepth {
		_, srvs, err := r.LookupSRV(ctx, "minecraft", "tcp", host)
		switch {
		case err != nil:
			debugf("SRV _minecraft._tcp.%s: %v", host, err)
		case len(srvs) > 0:
			target := strings.TrimSuffix(srvs[0].Target, ".")
			debugf("SRV _minecraft._tcp.%s: %s:%d", host, target, srvs[0].Port)
			if target != host {
				if addr, err := resolve(ctx, r, target, depth+1, debugf); err == nil {
					return addr, nil
				}
			}
		}
	}
	addrs, err := r.LookupHost(ctx, host)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", errors.New("no address found for " + host)
	}
	return addrs[0], nil
}

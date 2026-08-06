// Package netcalc is a pure-Go port of lib/netcalc.py (see
// reference/bash-seed/lib/netcalc.py for the original). Every function here
// mirrors one netcalc.py subcommand; the doc comments carry the same live-
// proven examples so the two implementations stay auditable against each
// other.
package netcalc

import (
	"fmt"
	"net"
)

// Nth returns the address at network-offset n inside cidr — i.e. the network
// address plus n. Mirrors `netcalc.py nth <cidr> <n>`.
//
// Example: Nth("10.31.0.48/28", 1) -> "10.31.0.49" (the ".1" gateway
// convention used for VLAN/route rendering).
//
// Returns an error if cidr does not parse, is not IPv4, or if the resulting
// address falls outside the network.
func Nth(cidr string, n int) (string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("netcalc: parse cidr %q: %w", cidr, err)
	}
	base4 := ipnet.IP.To4()
	if base4 == nil {
		return "", fmt.Errorf("netcalc: %q is not an IPv4 network", cidr)
	}
	base := uint32(base4[0])<<24 | uint32(base4[1])<<16 | uint32(base4[2])<<8 | uint32(base4[3])
	addr := base + uint32(n)                                                      // #nosec G115 -- n is caller-supplied small offset, not attacker-controlled
	result := net.IPv4(byte(addr>>24), byte(addr>>16), byte(addr>>8), byte(addr)) // #nosec G115 -- addr shift fits inside byte bounds
	if !ipnet.Contains(result) {
		return "", fmt.Errorf("netcalc: %s is outside %s", result, ipnet)
	}
	return result.String(), nil
}

// MACForIP returns the GCP virtio-derived MAC address for an internal IPv4
// address: "42:01:" + the hex encoding of each octet. Mirrors
// `netcalc.py mac-for-ip <ip>` — see reference/TRIAL-NOTES.md contract item 2.
//
// Example: MACForIP("10.31.0.49") -> "42:01:0a:1f:00:31".
func MACForIP(ip string) (string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", fmt.Errorf("netcalc: not an IP address: %q", ip)
	}
	v4 := parsed.To4()
	if v4 == nil {
		return "", fmt.Errorf("netcalc: not an IPv4 address: %q", ip)
	}
	return fmt.Sprintf("42:01:%02x:%02x:%02x:%02x", v4[0], v4[1], v4[2], v4[3]), nil
}

// InCIDR reports whether ip falls inside cidr. Mirrors
// `netcalc.py in-cidr <ip> <cidr>`.
func InCIDR(ip, cidr string) (bool, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false, fmt.Errorf("netcalc: not an IP address: %q", ip)
	}
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, fmt.Errorf("netcalc: parse cidr %q: %w", cidr, err)
	}
	return ipnet.Contains(parsed), nil
}

// CIDRAddr returns the network-address portion of cidr. Mirrors
// `netcalc.py cidr-addr <cidr>`.
//
// Example: CIDRAddr("10.66.0.0/16") -> "10.66.0.0".
func CIDRAddr(cidr string) (string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("netcalc: parse cidr %q: %w", cidr, err)
	}
	return ipnet.IP.String(), nil
}

// CIDRPrefixLen returns the prefix-length portion of cidr. Mirrors
// `netcalc.py cidr-prefixlen <cidr>`.
//
// Example: CIDRPrefixLen("10.66.0.0/16") -> 16.
func CIDRPrefixLen(cidr string) (int, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, fmt.Errorf("netcalc: parse cidr %q: %w", cidr, err)
	}
	ones, _ := ipnet.Mask.Size()
	return ones, nil
}

#!/usr/bin/env python3
"""Small IPv4 helpers used by the discover/install phase scripts.

Subcommands:
  nth <cidr> <n>       print the Nth address of the network (network+n),
                        e.g. `nth 10.31.0.48/28 1` -> 10.31.0.49 (the ".1"
                        gateway convention used for VLAN/route rendering)
  mac-for-ip <ip>      print the GCP virtio-derived MAC for an internal IP
                        (42:01:<hex-ip> convention — see
                        reference/TRIAL-NOTES.md contract item 2)
  in-cidr <ip> <cidr>  exit 0 if ip is inside cidr, else exit 1
  cidr-addr <cidr>     print the network address part of a CIDR
                        (10.66.0.0/16 -> 10.66.0.0) — envsubst can't do bash
                        parameter expansion, so route templates need this
                        precomputed rather than doing ${VAR%%/*} inline.
  cidr-prefixlen <cidr> print the prefix length part of a CIDR (-> 16)

Stdlib-only (ipaddress) — no PyYAML dependency, so this can run even before
config.sh proves PyYAML is installed.
"""
import ipaddress
import sys


def cmd_nth(cidr: str, n: str) -> str:
    net = ipaddress.ip_network(cidr, strict=False)
    addr = net.network_address + int(n)
    if addr not in net:
        raise ValueError(f"{addr} is outside {net}")
    return str(addr)


def cmd_mac_for_ip(ip: str) -> str:
    octets = [int(o) for o in ip.split(".")]
    if len(octets) != 4:
        raise ValueError(f"not an IPv4 address: {ip}")
    return "42:01:" + ":".join(f"{o:02x}" for o in octets)


def cmd_in_cidr(ip: str, cidr: str) -> bool:
    return ipaddress.ip_address(ip) in ipaddress.ip_network(cidr, strict=False)


def cmd_cidr_addr(cidr: str) -> str:
    return str(ipaddress.ip_network(cidr, strict=False).network_address)


def cmd_cidr_prefixlen(cidr: str) -> str:
    return str(ipaddress.ip_network(cidr, strict=False).prefixlen)


def main():
    if len(sys.argv) < 2:
        sys.stderr.write(__doc__)
        sys.exit(2)
    op = sys.argv[1]
    try:
        if op == "nth":
            print(cmd_nth(sys.argv[2], sys.argv[3]))
        elif op == "mac-for-ip":
            print(cmd_mac_for_ip(sys.argv[2]))
        elif op == "in-cidr":
            sys.exit(0 if cmd_in_cidr(sys.argv[2], sys.argv[3]) else 1)
        elif op == "cidr-addr":
            print(cmd_cidr_addr(sys.argv[2]))
        elif op == "cidr-prefixlen":
            print(cmd_cidr_prefixlen(sys.argv[2]))
        else:
            sys.stderr.write(f"netcalc.py: unknown subcommand {op!r}\n")
            sys.exit(2)
    except (IndexError, ValueError) as e:
        sys.stderr.write(f"netcalc.py: {e}\n")
        sys.exit(1)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Parse `gcloud compute instances describe --format=json` output.

Used by 25-discover.sh instead of jq (keeps the toolchain to python3 only).

Subcommands (all read JSON from a file path argument):
  nic-ip <json-file> <vpc-name>
      print the primary internal IP (networkIP) of the NIC attached to
      <vpc-name>, matched by the network self-link containing /networks/<vpc-name>
  alias-cidr <json-file> <range-name>
      print the aliasIpRanges[].ipCidrRange whose subnetworkRangeName equals
      <range-name>, searched across all NICs
"""
import json
import sys


def load(path):
    with open(path) as f:
        return json.load(f)


def nic_ip(path, vpc_name):
    doc = load(path)
    for nic in doc.get("networkInterfaces", []):
        network = nic.get("network", "")
        if network.rstrip("/").endswith(f"/networks/{vpc_name}"):
            print(nic["networkIP"])
            return
    sys.stderr.write(f"gcejson.py: no NIC attached to vpc {vpc_name!r}\n")
    sys.exit(1)


def alias_cidr(path, range_name):
    doc = load(path)
    for nic in doc.get("networkInterfaces", []):
        for alias in nic.get("aliasIpRanges", []) or []:
            if alias.get("subnetworkRangeName") == range_name:
                print(alias["ipCidrRange"])
                return
    sys.stderr.write(f"gcejson.py: no aliasIpRanges entry for range {range_name!r}\n")
    sys.exit(1)


def main():
    if len(sys.argv) < 3:
        sys.stderr.write(__doc__)
        sys.exit(2)
    op, path = sys.argv[1], sys.argv[2]
    if op == "nic-ip":
        nic_ip(path, sys.argv[3])
    elif op == "alias-cidr":
        alias_cidr(path, sys.argv[3])
    else:
        sys.stderr.write(f"gcejson.py: unknown subcommand {op!r}\n")
        sys.exit(2)


if __name__ == "__main__":
    main()

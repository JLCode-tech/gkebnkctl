#!/bin/sh
# BNK egress probe — run inside the captured "agent" pod. Shows the identity the
# outside world sees + reachability. The result FLIPS depending on whether BNK
# egress capture is ON (F5SPKEgress present) or OFF.
#
# Mounted into the pod at /demo via the `egress-probe` ConfigMap
# (see workload.yaml). One-shot; use watch.sh for a continuous on-screen view.
LINE="------------------------------------------------------------"
echo "$LINE"
echo " BNK EGRESS PROBE   pod=$(hostname)  ns=${POD_NS:-?}  podIP=$(hostname -i 2>/dev/null)"
echo "$LINE"
SRC=$(curl -s --max-time 8 https://ifconfig.me/ip 2>/dev/null)
echo " egress source IP the internet sees : ${SRC:-<unreachable>}"
echo -n " reach google.com                   : "
curl -s -o /dev/null -w "HTTP %{http_code}  (%{time_total}s)\n" --max-time 10 https://www.google.com 2>/dev/null || echo "UNREACHABLE"
echo -n " reach api.github.com               : "
curl -s -o /dev/null -w "HTTP %{http_code}  (%{time_total}s)\n" --max-time 10 https://api.github.com 2>/dev/null || echo "UNREACHABLE"
echo -n " reach 1.1.1.1 (policy target)      : "
curl -s -o /dev/null -w "HTTP %{http_code}\n" --connect-timeout 6 --max-time 8 https://1.1.1.1 2>/dev/null || echo "BLOCKED / timeout"
echo "$LINE"
echo " TIP: NAT-GW EIP => via BNK (controlled).  node public IP => direct (uncontrolled)."
echo "$LINE"

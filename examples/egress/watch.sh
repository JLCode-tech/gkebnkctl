#!/bin/sh
# Continuous view — leave running on-screen during the demo while you flip BNK
# egress ON/OFF from the operator terminal (or the Forge UI). Refreshes every 3s.
# Mounted into the pod at /demo via the `egress-probe` ConfigMap.
while true; do
	clear 2>/dev/null
	sh /demo/probe.sh
	sleep 3
done

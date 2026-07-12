#!/bin/sh
# Adversarial: attempt to exfiltrate data over the network.
# Contained by --network none: there is no route off the container.
if wget -T 3 -q -O /dev/null http://1.1.1.1/ 2>/dev/null; then
  echo REACHED_NETWORK
else
  echo BLOCKED
fi

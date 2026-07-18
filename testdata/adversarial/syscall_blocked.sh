#!/bin/sh
# Adversarial: attempt to create a new user namespace via unshare(2) — the first
# step of many container-escape techniques (a new userns can grant capabilities
# the job otherwise lacks). Contained by the seccomp whitelist, which denies
# unshare (not in the compiler allow-list) with EPERM.
#
# This is the one attack whose wall is *specifically* seccomp: on this kernel an
# unprivileged process CAN unshare a user namespace, so without the profile the
# call succeeds (REACHED_UNSHARE). Only the seccomp filter turns it into BLOCKED,
# which is what makes it evidence that the profile is applied and enforcing.
if unshare -Ur true 2>/dev/null; then
  echo REACHED_UNSHARE
else
  echo BLOCKED
fi

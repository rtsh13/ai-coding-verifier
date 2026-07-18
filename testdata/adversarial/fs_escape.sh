#!/bin/sh
# Adversarial: attempt to tamper with a system file outside the job's workdir.
# Contained by the non-root user (uid 1000) and the isolated container
# filesystem — there is no host mount to reach, and /etc is not writable.
if echo pwned > /etc/passwd 2>/dev/null; then
  echo WROTE_SYSTEM_FILE
else
  echo BLOCKED
fi

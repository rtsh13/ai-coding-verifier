#!/bin/sh
# Adversarial: fork bomb. Contained by the per-container pids limit (fork starts
# failing) and the job TTL (the whole attempt is killed), leaving the host and
# the pool unaffected.
while :; do
  sleep 60 &
done

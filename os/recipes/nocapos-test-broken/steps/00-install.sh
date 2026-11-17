#!/bin/bash
# The health check after an update must notice that NoCapOS doesn't answer
# and go back to the previous system by itself.
set -euo pipefail
echo broken >/usr/lib/nocapos/test-marker
mkdir -p /usr/lib/systemd/system/nocapos.service.d
printf '[Service]\nExecStart=\nExecStart=/bin/false\nRestart=no\n' >/usr/lib/systemd/system/nocapos.service.d/90-test-broken.conf

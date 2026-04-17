#!/bin/sh
set -eu

cd /app
mkdir -p data

echo "[lucrum] running initial fetch"
if /usr/local/bin/lucrum; then
    echo "[lucrum] initial fetch complete"
else
    echo "[lucrum] initial fetch failed; scheduler will continue"
fi

echo "[lucrum] starting 6-hour scheduler"
exec /usr/local/bin/supercronic /etc/lucrum.cron

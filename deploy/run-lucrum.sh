#!/bin/sh
set -eu

LOCK_FILE="${LOCK_FILE:-/app/data/.lucrum.lock}"
MAX_ATTEMPTS="${MAX_ATTEMPTS:-3}"
RETRY_SECONDS="${RETRY_SECONDS:-300}"
LUCRUM_BIN="${LUCRUM_BIN:-/usr/local/bin/lucrum}"

if ! command -v flock >/dev/null 2>&1; then
    echo "flock is required but not installed" >&2
    exit 1
fi

if flock -n -E 200 "$LOCK_FILE" sh -eu -c '
    bin="$1"
    max_attempts="$2"
    retry_seconds="$3"

    attempt=1
    while [ "$attempt" -le "$max_attempts" ]; do
        echo "lucrum attempt ${attempt}/${max_attempts}"
        if "$bin"; then
            echo "lucrum run completed"
            exit 0
        fi

        if [ "$attempt" -lt "$max_attempts" ]; then
            echo "lucrum failed, retrying in ${retry_seconds}s"
            sleep "$retry_seconds"
        fi

        attempt=$((attempt + 1))
    done

    echo "lucrum failed after ${max_attempts} attempts" >&2
    exit 1
' sh "$LUCRUM_BIN" "$MAX_ATTEMPTS" "$RETRY_SECONDS"; then
    status=0
else
    status="$?"
fi

if [ "$status" -eq 0 ]; then
    exit 0
fi

if [ "$status" -eq 200 ]; then
    echo "another lucrum run is already in progress; skipping"
    exit 0
fi

exit "$status"

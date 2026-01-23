#!/bin/sh
set -e

# If Litestream replica URL is configured, use Litestream for replication
if [ -n "$LITESTREAM_REPLICA_URL" ]; then
    echo "Litestream replication enabled: $LITESTREAM_REPLICA_URL"

    # Attempt to restore from existing backup (if exists)
    echo "Attempting to restore from backup..."
    litestream restore -if-replica-exists -config /etc/litestream.yml "$JOG_DATA_DIR/metadata.db" || true

    # Start JOG with Litestream replication
    echo "Starting JOG with Litestream replication..."
    exec litestream replicate -config /etc/litestream.yml -exec "jog server"
else
    # Start JOG without Litestream
    echo "Starting JOG (Litestream disabled)..."
    exec jog server
fi

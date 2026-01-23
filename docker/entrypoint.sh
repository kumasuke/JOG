#!/bin/sh
set -e

# If Litestream replica URL is configured, use Litestream for replication
if [ -n "$LITESTREAM_REPLICA_URL" ]; then
    echo "INFO: Litestream replication enabled: $LITESTREAM_REPLICA_URL"

    # Attempt to restore from existing backup (if exists)
    echo "INFO: Checking for existing metadata backup..."
    if litestream restore -if-replica-exists -config /etc/litestream.yml "$JOG_DATA_DIR/metadata.db" 2>&1; then
        echo "INFO: Metadata restored successfully from backup"
    else
        echo "INFO: No existing backup found or restore failed, starting with fresh database"
    fi

    # Start JOG with Litestream replication
    echo "INFO: Starting JOG with Litestream replication..."
    exec litestream replicate -config /etc/litestream.yml -exec "jog server"
else
    # Start JOG without Litestream
    echo "INFO: Starting JOG (Litestream disabled)..."
    exec jog server
fi

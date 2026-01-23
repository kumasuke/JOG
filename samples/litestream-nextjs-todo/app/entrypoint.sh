#!/bin/sh
set -e

DATABASE_PATH="${DATABASE_PATH:-/data/todos.db}"

# If Litestream is configured, restore from backup
if [ -n "$LITESTREAM_S3_ENDPOINT" ]; then
    echo "Attempting to restore database from Litestream backup..."
    litestream restore -if-replica-exists -config /etc/litestream.yml "$DATABASE_PATH" || true
fi

# Run database migrations using sqlite3
echo "Running database migrations..."
if [ -f "$DATABASE_PATH" ]; then
    echo "Database exists, checking tables..."
else
    echo "Creating new database..."
fi

# Apply migrations directly with sqlite3
sqlite3 "$DATABASE_PATH" <<'EOF'
CREATE TABLE IF NOT EXISTS `todos` (
    `id` integer PRIMARY KEY AUTOINCREMENT NOT NULL,
    `title` text NOT NULL,
    `completed` integer DEFAULT false NOT NULL,
    `created_at` integer NOT NULL
);
EOF
echo "Migration completed"

# Start the application
if [ -n "$LITESTREAM_S3_ENDPOINT" ]; then
    echo "Starting application with Litestream replication..."
    exec litestream replicate -config /etc/litestream.yml -exec "node server.js"
else
    echo "Starting application without Litestream..."
    exec node server.js
fi

#!/bin/sh

set -e # Exit immediately if a command exits with a non-zero status

# Start Redis in the background
echo "Starting Redis server..."
# Bind to 127.0.0.1 for internal access within the same container
redis-server --port 6379 --bind 127.0.0.1 &
REDIS_PID=$! # Store the PID of the Redis server process
# Ensure Redis is stopped when this script exits for any reason
trap 'kill "$REDIS_PID" >/dev/null 2>&1 || true' EXIT

# Wait for Redis to be ready (e.g., by trying to connect to it)
echo "Waiting for Redis to be ready..."
until redis-cli -h 127.0.0.1 -p 6379 ping >/dev/null 2>&1; do
    echo "Redis is unavailable - sleeping"
    sleep 1
done
echo "Redis is up and running!"

# Optional data path used by some tests
echo "$datapath"

# Run integration tests WITHOUT coverage
echo "Running Go integration tests (no coverage, tags=integration)..."
go test -vet=off -tags=integration -timeout 600s -v ./infs/integrationtests
TEST_STATUS=$?

echo "Tests finished with status: $TEST_STATUS"

# Exit with the status of the test command
exit $TEST_STATUS

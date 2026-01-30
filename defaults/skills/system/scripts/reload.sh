#!/bin/bash
# reload.sh - A script to send the SIGUSR1 signal to the kaggen process.

set -e

PROCESS_NAME="kaggen"

# Find the PID of the kaggen process.
# We exclude the PID of the grep process itself.
PID=$(ps aux | grep "[k]aggen" | awk '{print $2}')

if [ -z "$PID" ]; then
  echo "Error: Could not find the kaggen process."
  exit 1
fi

echo "Found kaggen process with PID: $PID"
echo "Sending SIGUSR1 signal to trigger skill reload..."

kill -SIGUSR1 "$PID"

echo "Signal sent successfully."

#!/usr/bin/env bash
set -euo pipefail

KEY_PATH="/Users/stephanfeb/claude-projects/amelia-autonomous-bot/gen-lang-client-0268081540-20a0ba9386a3.json"

if [[ "${1:-}" == "--help" ]]; then
    echo "Usage: auth.sh"
    echo ""
    echo "Authenticates with Google Cloud using the service account key."
    exit 0
fi

if [[ ! -f "$KEY_PATH" ]]; then
    echo "Error: Service account key not found at $KEY_PATH" >&2
    exit 1
fi

gcloud auth activate-service-account --key-file="$KEY_PATH"
echo "Successfully authenticated using service account key."

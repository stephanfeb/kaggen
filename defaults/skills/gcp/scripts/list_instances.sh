#!/usr/bin/env bash
set -euo pipefail

PROJECT_ID="gen-lang-client-0268081540"

if [[ "${1:-}" == "--help" ]]; then
    echo "Usage: list_instances.sh"
    echo ""
    echo "Lists all VM instances in the project $PROJECT_ID."
    exit 0
fi

echo "Fetching VM instances for project $PROJECT_ID..."
gcloud compute instances list --project="$PROJECT_ID"

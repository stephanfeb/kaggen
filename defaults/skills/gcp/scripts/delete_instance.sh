#!/usr/bin/env bash
set -euo pipefail

PROJECT_ID="gen-lang-client-0268081540"
ZONE="asia-southeast1-a"

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: delete_instance.sh <instance_name>"
    echo ""
    echo "Deletes a specified VM instance in $ZONE."
    exit 0
fi

INSTANCE_NAME="$1"

echo "Deleting instance $INSTANCE_NAME in project $PROJECT_ID ($ZONE)..."
gcloud compute instances delete "$INSTANCE_NAME" \
    --project="$PROJECT_ID" \
    --zone="$ZONE" \
    --quiet

echo "Instance $INSTANCE_NAME deleted successfully."

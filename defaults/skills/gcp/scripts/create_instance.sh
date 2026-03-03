#!/usr/bin/env bash
set -euo pipefail

PROJECT_ID="gen-lang-client-0268081540"
ZONE="asia-southeast1-a"
MACHINE_TYPE="e2-micro"

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: create_instance.sh <instance_name>"
    echo ""
    echo "Creates a lightweight $MACHINE_TYPE instance in $ZONE."
    exit 0
fi

INSTANCE_NAME="$1"

echo "Creating instance $INSTANCE_NAME in project $PROJECT_ID ($ZONE)..."
gcloud compute instances create "$INSTANCE_NAME" \
    --project="$PROJECT_ID" \
    --zone="$ZONE" \
    --machine-type="$MACHINE_TYPE" \
    --format="table(name, zone, status, networkInterfaces[0].accessConfigs[0].natIP)"

echo "Instance $INSTANCE_NAME created successfully."

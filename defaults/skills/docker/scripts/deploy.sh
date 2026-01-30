#!/bin/bash
set -e
# --- Argument Parsing ---
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --project-dir) PROJECT_DIR="$2"; shift ;;
        --container-name) CONTAINER_NAME="$2"; shift ;;
        --port-mapping) PORT_MAPPING="$2"; shift ;;
        --volume-name) VOLUME_NAME="$2"; shift ;;
        --volume-path) VOLUME_PATH="$2"; shift ;;
        *) echo "Unknown parameter passed: $1"; exit 1 ;;
    esac
    shift
done
# --- Main Logic ---
echo "Deploying Docker container..."
cd "$PROJECT_DIR"
docker build -t "$CONTAINER_NAME" .
if [ -n "$VOLUME_NAME" ] && [ -n "$VOLUME_PATH" ]; then
    docker volume create "$VOLUME_NAME"
fi
docker stop "$CONTAINER_NAME" 2>/dev/null || true
docker rm "$CONTAINER_NAME" 2>/dev/null || true
if [ -n "$VOLUME_NAME" ] && [ -n "$VOLUME_PATH" ]; then
    docker run -d --name "$CONTAINER_NAME" -p "$PORT_MAPPING" -v "$VOLUME_NAME":"$VOLUME_PATH" "$CONTAINER_NAME"
else
    docker run -d --name "$CONTAIN_NAME" -p "$PORT_MAPPING" "$CONTAINER_NAME"
fi
echo "Container deployed successfully!"

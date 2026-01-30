#!/usr/bin/env bash
set -euo pipefail

# Browser Raw API Handler
# Sends raw JSON requests to the browser API for advanced use cases

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    cat <<'EOF'
Usage: raw.sh [json_payload]

Send a raw JSON request to the browser API for advanced use cases.

If no argument is provided, reads JSON from stdin.

JSON format (any valid browser API request):
{
  "action": "navigate",
  "url": "https://example.com"
}

Examples:
  # Direct argument
  bash scripts/raw.sh '{"action":"navigate","url":"https://example.com"}'

  # From stdin
  echo '{"action":"getTitle"}' | bash scripts/raw.sh

  # From file
  cat request.json | bash scripts/raw.sh

Output:
Returns the raw JSON response from the browser API, including timing information.

EOF
    exit 0
fi

# Verify tools
for cmd in curl jq; do
    if ! command -v "$cmd" &> /dev/null; then
        echo "Error: required tool not found: $cmd" >&2
        exit 1
    fi
done

# Configuration
API_URL="${BROWSER_API_URL:-http://localhost:3000/api/browser/action}"
TIMEOUT="${BROWSER_TIMEOUT:-30}"

# Get payload from argument or stdin
if [[ $# -gt 0 ]]; then
    PAYLOAD="$1"
else
    PAYLOAD=$(cat)
fi

# Validate JSON
if ! echo "$PAYLOAD" | jq empty 2>/dev/null; then
    jq -n \
        --arg error "Invalid JSON payload" \
        --arg payload "$PAYLOAD" \
        '{success: false, error: $error, raw_payload: $payload, duration_ms: 0}'
    exit 1
fi

# Send request to API
start_time=$(date +%s%N)

RESPONSE=$(curl -s \
    -w '\n%{http_code}' \
    -X POST \
    -H "Content-Type: application/json" \
    --max-time "$TIMEOUT" \
    -d "$PAYLOAD" \
    "$API_URL" 2>&1)

end_time=$(date +%s%N)
duration_ms=$(( (end_time - start_time) / 1000000 ))

# Extract status code and body
http_code=$(echo "$RESPONSE" | tail -n1)
body=$(echo "$RESPONSE" | sed '$d')

# Handle connection errors
if [[ -z "$body" ]] || [[ "$http_code" == "000" ]]; then
    jq -n \
        --arg error "Failed to connect to browser API at $API_URL" \
        --argjson duration_ms "$duration_ms" \
        '{success: false, error: $error, duration_ms: $duration_ms}'
    exit 1
fi

# Handle HTTP errors
if [[ "$http_code" != "200" ]]; then
    if echo "$body" | jq empty 2>/dev/null; then
        echo "$body" | jq --argjson duration_ms "$duration_ms" '. + {http_code: '"$http_code"', duration_ms: $duration_ms}'
    else
        jq -n \
            --arg error "HTTP $http_code" \
            --arg body "$body" \
            --argjson duration_ms "$duration_ms" \
            '{success: false, error: $error, http_code: '"$http_code"', raw_response: $body, duration_ms: $duration_ms}'
    fi
    exit 1
fi

# Output response
if echo "$body" | jq empty 2>/dev/null; then
    echo "$body" | jq --argjson duration_ms "$duration_ms" '. + {duration_ms: $duration_ms}'
else
    jq -n \
        --arg body "$body" \
        --argjson duration_ms "$duration_ms" \
        '{success: false, error: "Invalid JSON response", raw_response: $body, duration_ms: $duration_ms}'
    exit 1
fi

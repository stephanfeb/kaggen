#!/usr/bin/env bash
set -euo pipefail

# Browser Batch Operations Handler
# Executes multiple browser actions sequentially from a JSON file

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    cat <<'EOF'
Usage: batch.sh <json_file>

Execute multiple browser actions sequentially from a JSON file.

JSON format:
{
  "actions": [
    { "action": "navigate", "url": "https://example.com" },
    { "action": "waitForSelector", "selector": ".content" },
    { "action": "click", "selector": "button.expand" },
    { "action": "getText", "selector": "h1" }
  ]
}

Examples:
  bash scripts/batch.sh workflow.json

Output:
Returns a JSON object with:
  - success: true if all actions succeeded
  - results: array of individual action results
  - errors: array of error details (if any)
  - duration_ms: total execution time

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

JSON_FILE="$1"

# Validate file exists
if [[ ! -f "$JSON_FILE" ]]; then
    jq -n \
        --bool false \
        --arg error "JSON file not found: $JSON_FILE" \
        '{success: false, results: [], errors: [$error], duration_ms: 0}'
    exit 1
fi

# Validate JSON
if ! jq empty "$JSON_FILE" 2>/dev/null; then
    jq -n \
        --bool false \
        --arg error "Invalid JSON in file: $JSON_FILE" \
        '{success: false, results: [], errors: [$error], duration_ms: 0}'
    exit 1
fi

# Configuration
API_URL="${BROWSER_API_URL:-http://localhost:3000/api/browser/action}"
TIMEOUT="${BROWSER_TIMEOUT:-30}"

# Track results
start_time=$(date +%s%N)
results=()
errors=()
has_error=false

# Extract actions array
actions=$(jq -r '.actions // []' "$JSON_FILE")
action_count=$(echo "$actions" | jq 'length')

if [[ "$action_count" -eq 0 ]]; then
    jq -n \
        --bool false \
        --arg error "No actions found in JSON file" \
        '{success: false, results: [], errors: [$error], duration_ms: 0}'
    exit 1
fi

# Execute each action
for i in $(seq 0 $((action_count - 1))); do
    action_obj=$(echo "$actions" | jq ".[$i]")
    action_name=$(echo "$action_obj" | jq -r '.action')
    
    # Send request
    response=$(curl -s \
        -w '\n%{http_code}' \
        -X POST \
        -H "Content-Type: application/json" \
        --max-time "$TIMEOUT" \
        -d "$action_obj" \
        "$API_URL" 2>&1)
    
    http_code=$(echo "$response" | tail -n1)
    body=$(echo "$response" | sed '$d')
    
    # Check response
    if [[ -z "$body" ]] || [[ "$http_code" == "000" ]]; then
        error_msg="Failed to connect to browser API at $API_URL"
        errors+=("$error_msg")
        results+=($(jq -n --arg action "$action_name" --arg error "$error_msg" '{action: $action, success: false, error: $error}'))
        has_error=true
        continue
    fi
    
    if [[ "$http_code" != "200" ]]; then
        error_msg=$(echo "$body" | jq -r '.error // .message // "HTTP error"' 2>/dev/null || echo "HTTP error: $http_code")
        errors+=("$error_msg")
        results+=($(jq -n --arg action "$action_name" --arg error "$error_msg" '{action: $action, success: false, error: $error}'))
        has_error=true
        continue
    fi
    
    # Parse response
    if echo "$body" | jq empty 2>/dev/null; then
        results+=($(echo "$body" | jq --arg action "$action_name" 'if has("action") then . else . + {action: $action} end'))
        
        # Check if action itself failed
        success=$(echo "$body" | jq -r '.success // true')
        if [[ "$success" == "false" ]]; then
            error=$(echo "$body" | jq -r '.error // "Unknown error"')
            errors+=("$error")
            has_error=true
        fi
    else
        error_msg="Invalid JSON response for action: $action_name"
        errors+=("$error_msg")
        results+=($(jq -n --arg action "$action_name" --arg error "$error_msg" '{action: $action, success: false, error: $error}'))
        has_error=true
    fi
done

end_time=$(date +%s%N)
duration_ms=$(( (end_time - start_time) / 1000000 ))

# Build results array
results_json="["
for i in "${!results[@]}"; do
    results_json+="${results[$i]}"
    if [[ $i -lt $((${#results[@]} - 1)) ]]; then
        results_json+=","
    fi
done
results_json+="]"

# Build errors array
errors_json="["
for i in "${!errors[@]}"; do
    errors_json+="\"${errors[$i]}\""
    if [[ $i -lt $((${#errors[@]} - 1)) ]]; then
        errors_json+=","
    fi
done
errors_json+="]"

# Return final result
overall_success=true
if [[ "$has_error" == "true" ]]; then
    overall_success=false
fi

jq -n \
    --argjson success "$overall_success" \
    --argjson results "$results_json" \
    --argjson errors "$errors_json" \
    --argjson duration_ms "$duration_ms" \
    '{success: $success, results: $results, errors: $errors, duration_ms: $duration_ms, action_count: '"$action_count"'}'

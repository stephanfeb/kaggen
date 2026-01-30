#!/usr/bin/env bash
set -euo pipefail

# Browser Automation Skill - Main Command Handler
# Parses natural language-like commands and sends them to the browser API

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    cat <<'EOF'
Usage: browser.sh <action> [selector] [value] [options...]

Execute a single browser action and return structured JSON.

Actions (with examples):
  navigate <url>                    - Navigate to URL
  click <selector>                  - Click element
  type <selector> <text>            - Type into input
  screenshot [filename]             - Take screenshot
  getText <selector>                - Extract text
  getHTML <selector>                - Extract HTML
  getInnerHTML <selector>           - Get inner HTML
  getOuterHTML <selector>           - Get outer HTML
  evaluate <code>                   - Run JavaScript
  waitForSelector <selector> [ms]   - Wait for element
  waitForNavigation [timeout]       - Wait for page load
  getTitle                          - Get page title
  getCurrentUrl                     - Get current URL
  goBack                            - Back in history
  goForward                         - Forward in history
  reload                            - Reload page
  setCookie <name> <value> [opts]   - Set cookie
  getCookie [name]                  - Get cookie(s)
  clearCookies                      - Clear all cookies
  scrollTo <x> <y>                  - Scroll to position
  scrollIntoView <selector>         - Scroll element into view
  setViewport <width> <height>      - Set viewport
  getDimensions <selector>          - Get element size/position
  getAttribute <selector> <name>    - Get element attribute
  getProperties <selector>          - Get element properties

Examples:
  bash scripts/browser.sh navigate https://example.com
  bash scripts/browser.sh click "button.submit"
  bash scripts/browser.sh type "input[name=q]" "search term"
  bash scripts/browser.sh screenshot /tmp/page.png
  bash scripts/browser.sh getText "h1"
  bash scripts/browser.sh evaluate "document.title"

Set BROWSER_API_URL and BROWSER_TIMEOUT via environment:
  export BROWSER_API_URL="http://localhost:3000/api/browser/action"
  export BROWSER_TIMEOUT=30

EOF
    exit 0
fi

# Configuration
API_URL="${BROWSER_API_URL:-http://localhost:3000/api/browser/action}"
TIMEOUT="${BROWSER_TIMEOUT:-30}"

# Verify required tools
for cmd in curl jq; do
    if ! command -v "$cmd" &> /dev/null; then
        echo "Error: required tool not found: $cmd" >&2
        exit 1
    fi
done

# Extract action
ACTION="$1"
shift

# Parse arguments based on action and build payload
build_payload() {
    local action="$1"
    shift
    
    case "$action" in
        navigate)
            if [[ $# -lt 1 ]]; then
                echo "Error: navigate requires <url>" >&2
                return 1
            fi
            jq -n --arg url "$1" '{action: "navigate", params: {url: $url}}'
            ;;
        
        click)
            if [[ $# -lt 1 ]]; then
                echo "Error: click requires <selector>" >&2
                return 1
            fi
            jq -n --arg selector "$1" '{action: "click", params: {selector: $selector}}'
            ;;
        
        type)
            if [[ $# -lt 2 ]]; then
                echo "Error: type requires <selector> <text>" >&2
                return 1
            fi
            jq -n --arg selector "$1" --arg text "$2" '{action: "type", params: {selector: $selector, text: $text}}'
            ;;
        
        screenshot)
            if [[ $# -gt 0 ]]; then
                jq -n --arg filename "$1" '{action: "screenshot", params: {filename: $filename}}'
            else
                jq -n '{action: "screenshot", params: {}}'
            fi
            ;;
        
        getText)
            if [[ $# -lt 1 ]]; then
                echo "Error: getText requires <selector>" >&2
                return 1
            fi
            jq -n --arg selector "$1" '{action: "getText", params: {selector: $selector}}'
            ;;
        
        getHTML)
            if [[ $# -lt 1 ]]; then
                echo "Error: getHTML requires <selector>" >&2
                return 1
            fi
            jq -n --arg selector "$1" '{action: "getHTML", params: {selector: $selector}}'
            ;;
        
        getInnerHTML)
            if [[ $# -lt 1 ]]; then
                echo "Error: getInnerHTML requires <selector>" >&2
                return 1
            fi
            jq -n --arg selector "$1" '{action: "getInnerHTML", params: {selector: $selector}}'
            ;;
        
        getOuterHTML)
            if [[ $# -lt 1 ]]; then
                echo "Error: getOuterHTML requires <selector>" >&2
                return 1
            fi
            jq -n --arg selector "$1" '{action: "getOuterHTML", params: {selector: $selector}}'
            ;;
        
        evaluate)
            if [[ $# -lt 1 ]]; then
                echo "Error: evaluate requires <code>" >&2
                return 1
            fi
            jq -n --arg code "$1" '{action: "evaluate", params: {code: $code}}'
            ;;
        
        waitForSelector)
            if [[ $# -lt 1 ]]; then
                echo "Error: waitForSelector requires <selector> [timeout]" >&2
                return 1
            fi
            if [[ $# -gt 1 ]]; then
                jq -n --arg selector "$1" --argjson timeout "$2" '{action: "waitForSelector", params: {selector: $selector, timeout: $timeout}}'
            else
                jq -n --arg selector "$1" '{action: "waitForSelector", params: {selector: $selector}}'
            fi
            ;;
        
        waitForNavigation)
            if [[ $# -gt 0 ]]; then
                jq -n --argjson timeout "$1" '{action: "waitForNavigation", params: {timeout: $timeout}}'
            else
                jq -n '{action: "waitForNavigation", params: {}}'
            fi
            ;;
        
        getTitle)
            jq -n '{action: "getTitle", params: {}}'
            ;;
        
        getCurrentUrl)
            jq -n '{action: "getCurrentUrl", params: {}}'
            ;;
        
        goBack)
            jq -n '{action: "goBack", params: {}}'
            ;;
        
        goForward)
            jq -n '{action: "goForward", params: {}}'
            ;;
        
        reload)
            jq -n '{action: "reload", params: {}}'
            ;;
        
        setCookie)
            if [[ $# -lt 2 ]]; then
                echo "Error: setCookie requires <name> <value> [options...]" >&2
                return 1
            fi
            local name="$1"
            local value="$2"
            shift 2
            
            # Build cookie object with name and value
            local cookie_obj=$(jq -n --arg name "$name" --arg value "$value" '{name: $name, value: $value}')
            
            # Add optional properties (domain, path, etc.)
            while [[ $# -gt 0 ]]; do
                if [[ "$1" == *"="* ]]; then
                    local key="${1%%=*}"
                    local val="${1#*=}"
                    cookie_obj=$(echo "$cookie_obj" | jq --arg k "$key" --arg v "$val" '.[$k] = $v')
                fi
                shift
            done
            
            jq -n --argjson cookie "$cookie_obj" '{action: "setCookie", params: {cookie: $cookie}}'
            ;;
        
        getCookie)
            if [[ $# -gt 0 ]]; then
                jq -n --arg name "$1" '{action: "getCookie", params: {name: $name}}'
            else
                jq -n '{action: "getCookie", params: {}}'
            fi
            ;;
        
        clearCookies)
            jq -n '{action: "clearCookies", params: {}}'
            ;;
        
        scrollTo)
            if [[ $# -lt 2 ]]; then
                echo "Error: scrollTo requires <x> <y>" >&2
                return 1
            fi
            jq -n --argjson x "$1" --argjson y "$2" '{action: "scrollTo", params: {x: $x, y: $y}}'
            ;;
        
        scrollIntoView)
            if [[ $# -lt 1 ]]; then
                echo "Error: scrollIntoView requires <selector>" >&2
                return 1
            fi
            jq -n --arg selector "$1" '{action: "scrollIntoView", params: {selector: $selector}}'
            ;;
        
        setViewport)
            if [[ $# -lt 2 ]]; then
                echo "Error: setViewport requires <width> <height>" >&2
                return 1
            fi
            jq -n --argjson width "$1" --argjson height "$2" '{action: "setViewport", params: {width: $width, height: $height}}'
            ;;
        
        getDimensions)
            if [[ $# -lt 1 ]]; then
                echo "Error: getDimensions requires <selector>" >&2
                return 1
            fi
            jq -n --arg selector "$1" '{action: "getDimensions", params: {selector: $selector}}'
            ;;
        
        getAttribute)
            if [[ $# -lt 2 ]]; then
                echo "Error: getAttribute requires <selector> <name>" >&2
                return 1
            fi
            jq -n --arg selector "$1" --arg name "$2" '{action: "getAttribute", params: {selector: $selector, name: $name}}'
            ;;
        
        getProperties)
            if [[ $# -lt 1 ]]; then
                echo "Error: getProperties requires <selector>" >&2
                return 1
            fi
            jq -n --arg selector "$1" '{action: "getProperties", params: {selector: $selector}}'
            ;;
        
        *)
            echo "Error: unknown action: $action" >&2
            return 1
            ;;
    esac
}

# Build the payload
PAYLOAD=$(build_payload "$ACTION" "$@") || exit 1

# Send request to API - using seconds for better compatibility
start_time=$(date +%s)

RESPONSE=$(curl -s \
    -w '\n%{http_code}' \
    -X POST \
    -H "Content-Type: application/json" \
    --max-time "$TIMEOUT" \
    -d "$PAYLOAD" \
    "$API_URL" 2>&1)

end_time=$(date +%s)
duration_ms=$(( (end_time - start_time) * 1000 ))

# Extract status code and body
http_code=$(echo "$RESPONSE" | tail -n1)
body=$(echo "$RESPONSE" | sed '$d')

# Handle connection errors
if [[ -z "$body" ]] || [[ "$http_code" == "000" ]]; then
    jq -n \
        --bool false \
        --arg action "$ACTION" \
        --arg error "Failed to connect to browser API at $API_URL (is it running?)" \
        --argjson duration_ms "$duration_ms" \
        '{success: false, action: $action, data: null, error: $error, duration_ms: $duration_ms}'
    exit 1
fi

# Handle HTTP errors
if [[ "$http_code" != "200" ]]; then
    # Try to extract error message from response
    error_msg=$(echo "$body" | jq -r '.error // .message // "HTTP error"' 2>/dev/null || echo "HTTP error: $http_code")
    
    jq -n \
        --bool false \
        --arg action "$ACTION" \
        --arg error "$error_msg" \
        --argjson http_code "$http_code" \
        --argjson duration_ms "$duration_ms" \
        '{success: false, action: $action, data: null, error: $error, http_code: $http_code, duration_ms: $duration_ms}'
    exit 1
fi

# Parse and format response
if echo "$body" | jq empty 2>/dev/null; then
    # Valid JSON - add timing and action info
    # Special handling for screenshot action - ensure base64 field is preserved
    echo "$body" | jq --arg action "$ACTION" --argjson duration_ms "$duration_ms" \
        'if has("action") then . else . + {action: $action} end | . + {duration_ms: $duration_ms}'
else
    # Not JSON - wrap it
    jq -n \
        --bool false \
        --arg action "$ACTION" \
        --arg error "Invalid JSON response from API" \
        --arg response "$body" \
        --argjson duration_ms "$duration_ms" \
        '{success: false, action: $action, data: null, error: $error, raw_response: $response, duration_ms: $duration_ms}'
    exit 1
fi

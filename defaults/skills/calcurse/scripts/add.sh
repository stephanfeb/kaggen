#!/usr/bin/env bash
set -euo pipefail

show_help() {
    cat << 'EOF'
Usage: add.sh --type <apt|event|todo> --desc <description> [options]

Add a new appointment, event, or todo to calcurse.

Required:
  --type <type>       Type: apt (appointment), event, or todo
  --desc <text>       Description of the item

For appointments (--type apt):
  --date <date>       Date (required) - YYYY-MM-DD or today/tomorrow/etc
  --start <HH:MM>     Start time (required)
  --end <HH:MM>       End time (required)

For events (--type event):
  --date <date>       Date (required)

For todos (--type todo):
  --priority <1-9>    Priority (1=highest, 9=lowest, 0=none, default: 0)

Options:
  --note <text>       Attach a note to the item
  --help              Show this help

Examples:
  add.sh --type apt --date 2026-02-01 --start 10:00 --end 11:00 --desc "Team meeting"
  add.sh --type event --date 2026-02-14 --desc "Valentine's Day"
  add.sh --type todo --priority 1 --desc "Urgent task"
EOF
}

TYPE=""
DESC=""
DATE=""
START=""
END=""
PRIORITY="0"
NOTE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --type)     TYPE="$2"; shift 2 ;;
        --desc)     DESC="$2"; shift 2 ;;
        --date)     DATE="$2"; shift 2 ;;
        --start)    START="$2"; shift 2 ;;
        --end)      END="$2"; shift 2 ;;
        --priority) PRIORITY="$2"; shift 2 ;;
        --note)     NOTE="$2"; shift 2 ;;
        --help|-h)  show_help; exit 0 ;;
        *)          echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

# Validate required args
if [[ -z "$TYPE" ]]; then
    echo "Error: --type is required" >&2
    exit 1
fi

if [[ -z "$DESC" ]]; then
    echo "Error: --desc is required" >&2
    exit 1
fi

# Check calcurse is installed
if ! command -v calcurse &> /dev/null; then
    echo "Error: calcurse is not installed" >&2
    exit 1
fi

# Determine data directory
if [[ -d "$HOME/.calcurse" ]]; then
    DATA_DIR="$HOME/.calcurse"
elif [[ -d "${XDG_DATA_HOME:-$HOME/.local/share}/calcurse" ]]; then
    DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/calcurse"
else
    DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/calcurse"
    mkdir -p "$DATA_DIR"
fi

APTS_FILE="$DATA_DIR/apts"
TODO_FILE="$DATA_DIR/todo"

# Ensure files exist
touch "$APTS_FILE" "$TODO_FILE"

# Convert date to standard format if needed
convert_date() {
    local input="$1"
    case "$input" in
        today)     date +%m/%d/%Y ;;
        tomorrow)  date -v+1d +%m/%d/%Y 2>/dev/null || date -d "+1 day" +%m/%d/%Y ;;
        yesterday) date -v-1d +%m/%d/%Y 2>/dev/null || date -d "-1 day" +%m/%d/%Y ;;
        [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9])
            # Convert YYYY-MM-DD to MM/DD/YYYY for calcurse internal format
            echo "$input" | awk -F- '{print $2"/"$3"/"$1}'
            ;;
        *)
            echo "$input"
            ;;
    esac
}

case "$TYPE" in
    apt|appointment)
        if [[ -z "$DATE" || -z "$START" || -z "$END" ]]; then
            echo "Error: appointments require --date, --start, and --end" >&2
            exit 1
        fi
        
        FORMATTED_DATE=$(convert_date "$DATE")
        
        # Calculate timestamps (calcurse uses Unix timestamps internally)
        # Format: MM/DD/YYYY @ HH:MM -> MM/DD/YYYY @ HH:MM |description
        # Actual format in apts file: timestamp_start|timestamp_end|description
        # For simplicity, we'll use the pipe import method
        
        # Create a temp ical file and import it
        TEMP_ICS=$(mktemp /tmp/calcurse_add.XXXXXX.ics)
        
        # Parse date components
        if [[ "$DATE" =~ ^([0-9]{4})-([0-9]{2})-([0-9]{2})$ ]]; then
            YEAR="${BASH_REMATCH[1]}"
            MONTH="${BASH_REMATCH[2]}"
            DAY="${BASH_REMATCH[3]}"
        else
            # Handle relative dates
            PARSED=$(date +%Y-%m-%d 2>/dev/null)
            YEAR=$(echo "$PARSED" | cut -d- -f1)
            MONTH=$(echo "$PARSED" | cut -d- -f2)
            DAY=$(echo "$PARSED" | cut -d- -f3)
        fi
        
        START_HOUR=$(echo "$START" | cut -d: -f1)
        START_MIN=$(echo "$START" | cut -d: -f2)
        END_HOUR=$(echo "$END" | cut -d: -f1)
        END_MIN=$(echo "$END" | cut -d: -f2)
        
        cat > "$TEMP_ICS" << ICAL
BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
DTSTART:${YEAR}${MONTH}${DAY}T${START_HOUR}${START_MIN}00
DTEND:${YEAR}${MONTH}${DAY}T${END_HOUR}${END_MIN}00
SUMMARY:${DESC}
END:VEVENT
END:VCALENDAR
ICAL
        
        calcurse -i "$TEMP_ICS" -q
        rm -f "$TEMP_ICS"
        
        echo "Added appointment: $DESC on $DATE $START-$END"
        ;;
        
    event)
        if [[ -z "$DATE" ]]; then
            echo "Error: events require --date" >&2
            exit 1
        fi
        
        # Create temp ical for all-day event
        TEMP_ICS=$(mktemp /tmp/calcurse_add.XXXXXX.ics)
        
        if [[ "$DATE" =~ ^([0-9]{4})-([0-9]{2})-([0-9]{2})$ ]]; then
            YEAR="${BASH_REMATCH[1]}"
            MONTH="${BASH_REMATCH[2]}"
            DAY="${BASH_REMATCH[3]}"
        else
            PARSED=$(date +%Y-%m-%d)
            YEAR=$(echo "$PARSED" | cut -d- -f1)
            MONTH=$(echo "$PARSED" | cut -d- -f2)
            DAY=$(echo "$PARSED" | cut -d- -f3)
        fi
        
        cat > "$TEMP_ICS" << ICAL
BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
DTSTART;VALUE=DATE:${YEAR}${MONTH}${DAY}
SUMMARY:${DESC}
END:VEVENT
END:VCALENDAR
ICAL
        
        calcurse -i "$TEMP_ICS" -q
        rm -f "$TEMP_ICS"
        
        echo "Added event: $DESC on $DATE"
        ;;
        
    todo)
        # Append directly to todo file
        # Format: [priority] description
        if [[ "$PRIORITY" -gt 0 && "$PRIORITY" -le 9 ]]; then
            echo "[$PRIORITY] $DESC" >> "$TODO_FILE"
            echo "Added todo (priority $PRIORITY): $DESC"
        else
            echo "[0] $DESC" >> "$TODO_FILE"
            echo "Added todo: $DESC"
        fi
        ;;
        
    *)
        echo "Error: unknown type: $TYPE (use apt, event, or todo)" >&2
        exit 1
        ;;
esac

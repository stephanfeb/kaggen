#!/usr/bin/env bash
set -euo pipefail

show_help() {
    cat << 'EOF'
Usage: recur.sh <command> [options]

Manage recurring appointments and events.

Commands:
  add     Create a recurring item
  list    List recurring items

Add options:
  --type <apt|event>     Type of recurring item (required)
  --desc <text>          Description (required)
  --date <date>          Start date (required)
  --start <HH:MM>        Start time (for appointments)
  --end <HH:MM>          End time (for appointments)
  --recur <freq>         Frequency: daily, weekly, monthly, yearly (required)
  --until <date>         End date for recurrence (optional)
  --interval <n>         Repeat every n periods (default: 1)

Examples:
  recur.sh add --type apt --desc "Standup" --date 2026-02-02 --start 09:00 --end 09:30 --recur daily
  recur.sh add --type apt --desc "1-on-1" --date 2026-02-05 --start 14:00 --end 15:00 --recur weekly
  recur.sh add --type event --desc "Payday" --date 2026-02-15 --recur monthly
  recur.sh list
EOF
}

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]] || [[ "$1" == "-h" ]]; then
    show_help
    exit 0
fi

COMMAND="$1"
shift

# Check calcurse is installed
if ! command -v calcurse &> /dev/null; then
    echo "Error: calcurse is not installed" >&2
    exit 1
fi

case "$COMMAND" in
    list)
        echo "=== Recurring Appointments ==="
        calcurse -Q --filter-type recur-apt 2>/dev/null || echo "(none)"
        echo ""
        echo "=== Recurring Events ==="
        calcurse -Q --filter-type recur-event 2>/dev/null || echo "(none)"
        ;;
        
    add)
        TYPE=""
        DESC=""
        DATE=""
        START=""
        END=""
        RECUR=""
        UNTIL=""
        INTERVAL="1"
        
        while [[ $# -gt 0 ]]; do
            case "$1" in
                --type)     TYPE="$2"; shift 2 ;;
                --desc)     DESC="$2"; shift 2 ;;
                --date)     DATE="$2"; shift 2 ;;
                --start)    START="$2"; shift 2 ;;
                --end)      END="$2"; shift 2 ;;
                --recur)    RECUR="$2"; shift 2 ;;
                --until)    UNTIL="$2"; shift 2 ;;
                --interval) INTERVAL="$2"; shift 2 ;;
                *)          shift ;;
            esac
        done
        
        if [[ -z "$TYPE" || -z "$DESC" || -z "$DATE" || -z "$RECUR" ]]; then
            echo "Error: --type, --desc, --date, and --recur are required" >&2
            exit 1
        fi
        
        if [[ "$TYPE" == "apt" && ( -z "$START" || -z "$END" ) ]]; then
            echo "Error: appointments require --start and --end" >&2
            exit 1
        fi
        
        # Parse date
        if [[ "$DATE" =~ ^([0-9]{4})-([0-9]{2})-([0-9]{2})$ ]]; then
            YEAR="${BASH_REMATCH[1]}"
            MONTH="${BASH_REMATCH[2]}"
            DAY="${BASH_REMATCH[3]}"
        else
            echo "Error: date must be in YYYY-MM-DD format" >&2
            exit 1
        fi
        
        # Map recurrence to iCal RRULE
        case "$RECUR" in
            daily)   RRULE="RRULE:FREQ=DAILY;INTERVAL=$INTERVAL" ;;
            weekly)  RRULE="RRULE:FREQ=WEEKLY;INTERVAL=$INTERVAL" ;;
            monthly) RRULE="RRULE:FREQ=MONTHLY;INTERVAL=$INTERVAL" ;;
            yearly)  RRULE="RRULE:FREQ=YEARLY;INTERVAL=$INTERVAL" ;;
            *)       echo "Error: unknown recurrence: $RECUR" >&2; exit 1 ;;
        esac
        
        if [[ -n "$UNTIL" ]]; then
            if [[ "$UNTIL" =~ ^([0-9]{4})-([0-9]{2})-([0-9]{2})$ ]]; then
                UNTIL_FMT="${BASH_REMATCH[1]}${BASH_REMATCH[2]}${BASH_REMATCH[3]}"
                RRULE="$RRULE;UNTIL=${UNTIL_FMT}T235959"
            fi
        fi
        
        # Create iCal file
        TEMP_ICS=$(mktemp /tmp/calcurse_recur.XXXXXX.ics)
        
        if [[ "$TYPE" == "apt" ]]; then
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
${RRULE}
SUMMARY:${DESC}
END:VEVENT
END:VCALENDAR
ICAL
        else
            cat > "$TEMP_ICS" << ICAL
BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
DTSTART;VALUE=DATE:${YEAR}${MONTH}${DAY}
${RRULE}
SUMMARY:${DESC}
END:VEVENT
END:VCALENDAR
ICAL
        fi
        
        calcurse -i "$TEMP_ICS" -q
        rm -f "$TEMP_ICS"
        
        echo "Added recurring $TYPE: $DESC ($RECUR starting $DATE)"
        ;;
        
    *)
        echo "Error: unknown command: $COMMAND" >&2
        show_help
        exit 1
        ;;
esac

#!/usr/bin/env bash
set -euo pipefail

show_help() {
    cat << 'EOF'
Usage: todo.sh <command> [options]

Manage calcurse todo items.

Commands:
  list [options]              List todos
  add <description> [opts]    Add a new todo
  complete <pattern>          Mark matching todo as complete
  delete <pattern>            Delete matching todo

List options:
  --priority <n,n,...>   Filter by priority (comma-separated, 1-9, 0=none)
  --completed            Show only completed todos
  --uncompleted          Show only uncompleted todos

Add options:
  --priority <1-9>       Set priority (1=highest)

Examples:
  todo.sh list
  todo.sh list --priority 1,2,3 --uncompleted
  todo.sh add "Review code" --priority 2
  todo.sh complete "Review"
  todo.sh delete "old task"
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

# Determine data directory
if [[ -d "$HOME/.calcurse" ]]; then
    DATA_DIR="$HOME/.calcurse"
elif [[ -d "${XDG_DATA_HOME:-$HOME/.local/share}/calcurse" ]]; then
    DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/calcurse"
else
    DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/calcurse"
    mkdir -p "$DATA_DIR"
fi

TODO_FILE="$DATA_DIR/todo"
touch "$TODO_FILE"

case "$COMMAND" in
    list)
        PRIORITY=""
        COMPLETED=""
        
        while [[ $# -gt 0 ]]; do
            case "$1" in
                --priority)   PRIORITY="$2"; shift 2 ;;
                --completed)  COMPLETED="yes"; shift ;;
                --uncompleted) COMPLETED="no"; shift ;;
                *)            shift ;;
            esac
        done
        
        CALC_ARGS=(--filter-type todo)
        
        if [[ "$COMPLETED" == "yes" ]]; then
            CALC_ARGS+=(--filter-completed)
        elif [[ "$COMPLETED" == "no" ]]; then
            CALC_ARGS+=(--filter-uncompleted)
        fi
        
        OUTPUT=$(calcurse -Q "${CALC_ARGS[@]}" 2>/dev/null || echo "")
        
        if [[ -n "$PRIORITY" && -n "$OUTPUT" ]]; then
            # Filter by priority
            IFS=',' read -ra PRIOS <<< "$PRIORITY"
            for p in "${PRIOS[@]}"; do
                echo "$OUTPUT" | grep "^$p\." || true
            done
        else
            echo "$OUTPUT"
        fi
        ;;
        
    add)
        if [[ $# -lt 1 ]]; then
            echo "Error: description required" >&2
            exit 1
        fi
        
        DESC="$1"
        shift
        PRIORITY="0"
        
        while [[ $# -gt 0 ]]; do
            case "$1" in
                --priority) PRIORITY="$2"; shift 2 ;;
                *)          shift ;;
            esac
        done
        
        if [[ "$PRIORITY" -gt 0 && "$PRIORITY" -le 9 ]]; then
            echo "[$PRIORITY] $DESC" >> "$TODO_FILE"
            echo "Added todo (priority $PRIORITY): $DESC"
        else
            echo "[0] $DESC" >> "$TODO_FILE"
            echo "Added todo: $DESC"
        fi
        ;;
        
    complete)
        if [[ $# -lt 1 ]]; then
            echo "Error: pattern required" >&2
            exit 1
        fi
        
        PATTERN="$1"
        
        # Mark as complete by changing priority to negative or removing
        # In calcurse, completed todos have priority 0 and are marked differently
        # For simplicity, we'll prefix with [X] to indicate completion
        
        if grep -q "$PATTERN" "$TODO_FILE"; then
            # Create temp file
            TEMP_FILE=$(mktemp)
            while IFS= read -r line; do
                if [[ "$line" =~ $PATTERN ]] && [[ ! "$line" =~ ^\[X\] ]]; then
                    # Mark first match as complete
                    echo "[X]${line#\[*\]}"
                    PATTERN="____NOMATCH____"  # Don't match again
                else
                    echo "$line"
                fi
            done < "$TODO_FILE" > "$TEMP_FILE"
            mv "$TEMP_FILE" "$TODO_FILE"
            echo "Marked as complete: items matching '$1'"
        else
            echo "No matching todo found: $PATTERN"
        fi
        ;;
        
    delete)
        if [[ $# -lt 1 ]]; then
            echo "Error: pattern required" >&2
            exit 1
        fi
        
        PATTERN="$1"
        
        if grep -q "$PATTERN" "$TODO_FILE"; then
            TEMP_FILE=$(mktemp)
            DELETED=false
            while IFS= read -r line; do
                if [[ "$line" =~ $PATTERN ]] && [[ "$DELETED" == "false" ]]; then
                    DELETED=true
                    echo "Deleted: $line" >&2
                else
                    echo "$line"
                fi
            done < "$TODO_FILE" > "$TEMP_FILE"
            mv "$TEMP_FILE" "$TODO_FILE"
        else
            echo "No matching todo found: $PATTERN"
        fi
        ;;
        
    *)
        echo "Error: unknown command: $COMMAND" >&2
        show_help
        exit 1
        ;;
esac

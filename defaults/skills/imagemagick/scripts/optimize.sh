#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: optimize.sh <input> [output] [--max-width N] [--max-height N] [--quality N]"
    echo ""
    echo "Optimize an image for web. Strips metadata, resizes if needed, compresses."
    echo "If output is omitted, overwrites input."
    exit 0
fi

INPUT="$1"
shift

if [[ ! -f "$INPUT" ]]; then
    echo "Error: input file not found: $INPUT" >&2
    exit 1
fi

OUTPUT=""
MAX_W=""
MAX_H=""
QUALITY="80"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --max-width)  MAX_W="$2"; shift 2 ;;
        --max-height) MAX_H="$2"; shift 2 ;;
        --quality)    QUALITY="$2"; shift 2 ;;
        -*)           shift ;;
        *)
            if [[ -z "$OUTPUT" ]]; then
                OUTPUT="$1"
            fi
            shift
            ;;
    esac
done

[[ -z "$OUTPUT" ]] && OUTPUT="$INPUT"

ARGS=(-strip -quality "$QUALITY")

if [[ -n "$MAX_W" && -n "$MAX_H" ]]; then
    ARGS+=(-resize "${MAX_W}x${MAX_H}>")
elif [[ -n "$MAX_W" ]]; then
    ARGS+=(-resize "${MAX_W}x>")
elif [[ -n "$MAX_H" ]]; then
    ARGS+=(-resize "x${MAX_H}>")
fi

BEFORE=$(wc -c < "$INPUT" | tr -d ' ')
echo "Optimizing: $INPUT -> $OUTPUT"
magick "$INPUT" "${ARGS[@]}" "$OUTPUT"

AFTER=$(wc -c < "$OUTPUT" | tr -d ' ')
SAVED=$((BEFORE - AFTER))
echo "Done. Before: ${BEFORE} bytes, After: ${AFTER} bytes (saved ${SAVED} bytes)"

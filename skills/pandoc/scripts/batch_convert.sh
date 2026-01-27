#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 4 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: batch_convert.sh <input_dir> <output_dir> <from_ext> <to_ext> [extra flags...]"
    echo ""
    echo "Convert all files with <from_ext> in <input_dir> to <to_ext> in <output_dir>."
    echo ""
    echo "Examples:"
    echo "  batch_convert.sh ./docs ./output md html --standalone"
    echo "  batch_convert.sh ./word ./markdown docx md --wrap=none"
    exit 0
fi

INPUT_DIR="$1"
OUTPUT_DIR="$2"
FROM_EXT="$3"
TO_EXT="$4"
shift 4

if [[ ! -d "$INPUT_DIR" ]]; then
    echo "Error: input directory not found: $INPUT_DIR" >&2
    exit 1
fi

mkdir -p "$OUTPUT_DIR"

COUNT=0
FAILED=0

for INPUT_FILE in "$INPUT_DIR"/*."$FROM_EXT"; do
    [[ -f "$INPUT_FILE" ]] || continue

    BASENAME=$(basename "$INPUT_FILE" ".$FROM_EXT")
    OUTPUT_FILE="$OUTPUT_DIR/$BASENAME.$TO_EXT"

    echo "Converting: $INPUT_FILE -> $OUTPUT_FILE"
    if pandoc "$INPUT_FILE" -o "$OUTPUT_FILE" "$@" 2>&1; then
        COUNT=$((COUNT + 1))
    else
        echo "  FAILED" >&2
        FAILED=$((FAILED + 1))
    fi
done

echo ""
echo "Done. Converted: $COUNT, Failed: $FAILED"

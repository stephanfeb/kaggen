#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: info.sh <file>"
    echo ""
    echo "Show image metadata: format, dimensions, color space, file size, bit depth."
    exit 0
fi

FILE="$1"
if [[ ! -f "$FILE" ]]; then
    echo "Error: file not found: $FILE" >&2
    exit 1
fi

echo "File: $FILE"
echo "Size: $(wc -c < "$FILE" | tr -d ' ') bytes"
echo ""

magick identify -verbose "$FILE" 2>/dev/null | grep -E '^\s*(Format|Geometry|Colorspace|Depth|Type|Resolution|Units|Filesize|Compression|Quality):' || true

echo ""
echo "=== EXIF ==="
magick identify -verbose "$FILE" 2>/dev/null | sed -n '/exif:/p' | head -20 || echo "(no EXIF data)"

#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]] || [[ "$1" == "--help" ]]; then
    echo "Usage: inspect.sh <file>"
    echo ""
    echo "Show document metadata, word count, and structure."
    exit 0
fi

FILE="$1"
if [[ ! -f "$FILE" ]]; then
    echo "Error: file not found: $FILE" >&2
    exit 1
fi

EXT="${FILE##*.}"
echo "File: $FILE"
echo "Format: $EXT"
echo "Size: $(wc -c < "$FILE" | tr -d ' ') bytes"
echo ""

# Word count via pandoc plain text conversion
WORDS=$(pandoc "$FILE" -t plain 2>/dev/null | wc -w | tr -d ' ')
echo "Word count: $WORDS"
echo ""

# Extract metadata
echo "=== Metadata ==="
pandoc "$FILE" --template='title: $title$
author: $author$
date: $date$
lang: $lang$' 2>/dev/null || echo "(no metadata)"
echo ""

# Extract headings structure
echo "=== Structure ==="
pandoc "$FILE" -t json 2>/dev/null | python3 -c "
import json, sys
try:
    doc = json.load(sys.stdin)
    blocks = doc.get('blocks', [])
    for b in blocks:
        if b.get('t') == 'Header':
            level = b['c'][0]
            text = ''.join(
                i.get('c', '') if isinstance(i.get('c'), str) else
                i.get('c', [''])[0] if isinstance(i.get('c'), list) and i['c'] else ''
                for i in b['c'][2]
            )
            indent = '  ' * (level - 1)
            print(f'{indent}H{level}: {text}')
except Exception:
    print('(could not parse structure)')
" 2>/dev/null || echo "(structure extraction requires python3)"

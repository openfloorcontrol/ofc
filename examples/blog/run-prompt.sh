#!/bin/bash
set -e

if [ $# -lt 2 ]; then
    echo "Usage: $0 <prompt-file> <message>"
    echo "Example: $0 prompt.txt 'analyze sales.csv and give me a projection'"
    exit 1
fi

PROMPT_FILE="$1"
shift
MESSAGE="$*"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OFC="$SCRIPT_DIR/../../cli/ofc"

# Reset workspace and copy data files
rm -f "$SCRIPT_DIR/workspace/"*
cp "$SCRIPT_DIR/sales.csv" "$SCRIPT_DIR/workspace/"

# Patch blueprint to use the specified prompt file
sed "s|prompt_file: .*|prompt_file: $PROMPT_FILE|" "$SCRIPT_DIR/blueprint.yaml" > "$SCRIPT_DIR/blueprint-run.yaml"

# Run ofc with the message
cd "$SCRIPT_DIR"
"$OFC" run -f blueprint-run.yaml "$MESSAGE"

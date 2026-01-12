#!/bin/bash
# Log collector for HOG Local Stack
# Pipes container logs to Loki via HTTP API

LOKI_URL="http://localhost:3100/loki/api/v1/push"
CONTAINER_NAME="hog-local-stack_hog_1"

echo "🔄 Starting log collector for $CONTAINER_NAME..."
echo "   Pushing logs to $LOKI_URL"
echo "   Press Ctrl+C to stop"
echo ""

# Function to push log to Loki
push_log() {
    local log_line="$1"
    local timestamp=$(date +%s%N)

    # Extract fields from JSON log if possible
    local service="hog-gateway"
    local level=$(echo "$log_line" | jq -r '.level // "INFO"' 2>/dev/null || echo "INFO")
    local trace_id=$(echo "$log_line" | jq -r '.trace_id // ""' 2>/dev/null || echo "")

    # Build Loki push payload
    local payload=$(cat <<EOF
{
  "streams": [
    {
      "stream": {
        "job": "hog",
        "service": "$service",
        "level": "$level",
        "container": "$CONTAINER_NAME"
      },
      "values": [
        ["$timestamp", $(echo "$log_line" | jq -Rs .)]
      ]
    }
  ]
}
EOF
)

    # Push to Loki (silent, ignore errors)
    curl -s -X POST "$LOKI_URL" \
        -H "Content-Type: application/json" \
        -d "$payload" > /dev/null 2>&1
}

# Follow container logs and push each line
podman logs -f "$CONTAINER_NAME" 2>&1 | while IFS= read -r line; do
    # Skip empty lines
    [ -z "$line" ] && continue

    # Echo to terminal for visibility
    echo "$line"

    # Push to Loki in background
    push_log "$line" &
done

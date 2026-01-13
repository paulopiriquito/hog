#!/bin/sh
set -e

# =============================================================================
# HOG Gateway Entrypoint
# Handles flexible configuration, environment overlays, and validation
# =============================================================================

# Configuration paths
CONFIG_DIR="/etc/krakend"
CONFIG_FILE="${CONFIG_DIR}/config/krakend.tmpl"
SETTINGS_DIR="${CONFIG_DIR}/settings"
PARTIALS_DIR="${CONFIG_DIR}/partials"
TEMPLATES_DIR="${CONFIG_DIR}/templates"

# Environment defaults
HOG_ENV="${HOG_ENV:-local}"
USE_OAUTH="${USE_OAUTH:-0}"

echo "[HOG] Starting gateway with environment: ${HOG_ENV}"

# =============================================================================
# Validate OAuth secrets if enabled
# =============================================================================
if [ "${USE_OAUTH}" = "1" ]; then
    echo "[HOG] OAuth enabled - validating required secrets..."

    if [ -z "${AUTH_COOKIE_KEY}" ]; then
        echo "[HOG] ERROR: AUTH_COOKIE_KEY is required when USE_OAUTH=1"
        echo "[HOG] Please set a 32-byte secret key for session encryption"
        exit 1
    fi

    if [ -z "${IDP_CLIENT_SECRET}" ]; then
        echo "[HOG] ERROR: IDP_CLIENT_SECRET is required when USE_OAUTH=1"
        echo "[HOG] Please set your OAuth client secret"
        exit 1
    fi

    # Validate AUTH_COOKIE_KEY length (should be 32 bytes)
    KEY_LENGTH=$(printf '%s' "${AUTH_COOKIE_KEY}" | wc -c | tr -d ' ')
    if [ "${KEY_LENGTH}" -lt 32 ]; then
        echo "[HOG] WARNING: AUTH_COOKIE_KEY should be at least 32 characters (current: ${KEY_LENGTH})"
    fi

    echo "[HOG] OAuth secrets validated"
fi

# =============================================================================
# Configure Flexible Configuration
# =============================================================================
export FC_ENABLE=1
export FC_PARTIALS="${PARTIALS_DIR}"
export FC_TEMPLATES="${PARTIALS_DIR}"
export FC_OUT="${CONFIG_DIR}/compiled/krakend.json"

# Settings directory - KrakenD loads all .json files from this directory
# Each file's name (without extension) becomes the key to access its contents
export FC_SETTINGS="${CONFIG_DIR}/active-settings"

# Ensure directories exist
mkdir -p "${CONFIG_DIR}/compiled"
mkdir -p "${CONFIG_DIR}/active-settings"

# Verify environment settings file exists
if [ ! -f "${SETTINGS_DIR}/${HOG_ENV}.json" ]; then
    echo "[HOG] ERROR: Settings file not found: ${SETTINGS_DIR}/${HOG_ENV}.json"
    echo "[HOG] Available environments: local, nprod, prod"
    exit 1
fi

# Copy the selected environment settings to the active settings directory
# This file will be accessible in templates via {{ .service.property_name }}
cp "${SETTINGS_DIR}/${HOG_ENV}.json" "${CONFIG_DIR}/active-settings/service.json"

echo "[HOG] Using settings: ${HOG_ENV}"

# =============================================================================
# Validate Configuration
# =============================================================================
echo "[HOG] Validating configuration..."

if ! /usr/bin/krakend check -c "${CONFIG_FILE}"; then
    echo "[HOG] ERROR: Configuration validation failed"
    exit 1
fi

echo "[HOG] Configuration valid"

# =============================================================================
# Start Gateway or Run Command
# =============================================================================
# If arguments are passed, execute them (e.g., "check" for testing)
# Otherwise, start the gateway
if [ $# -gt 0 ]; then
    echo "[HOG] Executing command: $@"
    exec /usr/bin/krakend "$@"
else
    echo "[HOG] Starting KrakenD gateway..."
    exec /usr/bin/krakend run -c "${CONFIG_FILE}"
fi

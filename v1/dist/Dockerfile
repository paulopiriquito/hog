# =============================================================================
# HOG Intermediate Image
# =============================================================================
# This image extends the base HOG gateway with:
# - Flexible YAML configuration with environment overlays
# - Pre-configured plugins (authenticator, static-content)
# - Reusable configuration fragments
# - Conditional OAuth validation
#
# Usage:
#   FROM ghcr.io/paulopiriquito/hog:intermediate
#   COPY ./my-partials/ /etc/krakend/partials/custom/
#   ENV HOG_ENV=prod
# =============================================================================

FROM ghcr.io/paulopiriquito/hog:latest

LABEL maintainer="paulo.piriquito@outlook.pt"
LABEL description="HOG Gateway - Intermediate image with flexible configuration"

# Install shell for entrypoint script
USER root
RUN apk add --no-cache bash

# Create directory structure
RUN mkdir -p /etc/krakend/config \
             /etc/krakend/partials \
             /etc/krakend/settings \
             /etc/krakend/custom \
             /etc/krakend/templates \
             /etc/krakend/compiled \
             /etc/krakend/active-settings && \
    chown -R krakend:nogroup /etc/krakend

# Copy configuration files
COPY --chown=krakend:nogroup config/ /etc/krakend/config/
COPY --chown=krakend:nogroup partials/ /etc/krakend/partials/
COPY --chown=krakend:nogroup settings/ /etc/krakend/settings/

# Copy and set entrypoint script
COPY --chown=krakend:nogroup entrypoint.sh /etc/krakend/entrypoint.sh
RUN chmod +x /etc/krakend/entrypoint.sh

# Switch back to non-root user
USER 1000

# =============================================================================
# Environment Variables
# =============================================================================
# Core settings
ENV HOG_ENV=local
ENV USE_OAUTH=0

# Flexible config enabled by default
ENV FC_ENABLE=1

# Service defaults (can be overridden)
ENV SERVICE_NAME=hog-gateway
ENV PORT=8080

# Disable KrakenD usage reporting
ENV USAGE_DISABLE=1

# =============================================================================
# Runtime
# =============================================================================
WORKDIR /etc/krakend

ENTRYPOINT ["/etc/krakend/entrypoint.sh"]

EXPOSE 8080 8090

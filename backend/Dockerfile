# Dockerfile for MediaVault Backend
# This is a runtime-only Dockerfile - the binary is built by Encore CLI externally
# Use this with: encore build docker ghcr.io/owner/repo/mediavault-backend:tag --base debian:bookworm-slim

FROM debian:bookworm-slim

LABEL org.opencontainers.image.source="https://github.com/hitomihiumi/surtr-media"
LABEL org.opencontainers.image.description="MediaVault Backend Service"
LABEL org.opencontainers.image.licenses="MIT"

# Install runtime dependencies including FFMPEG for processing service
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    libx265-dev \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

# Verify FFMPEG installation
RUN ffmpeg -version && ffmpeg -encoders 2>/dev/null | grep libx265

# Create non-root user
RUN groupadd --gid 1000 appgroup && \
    useradd --uid 1000 --gid appgroup --shell /bin/bash --create-home appuser

# Create temp directory for processing
RUN mkdir -p /tmp/processing && \
    chown -R appuser:appgroup /tmp/processing

# Set working directory
WORKDIR /app

# The application binary and migrations will be added by Encore's build process
# This Dockerfile serves as documentation and for building a custom base image

# Environment variables
ENV FFMPEG_BINARY=/usr/bin/ffmpeg
ENV FFPROBE_BINARY=/usr/bin/ffprobe
ENV TEMP_DIR=/tmp/processing

# Expose default Encore port
EXPOSE 4000

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:4000/healthz || exit 1

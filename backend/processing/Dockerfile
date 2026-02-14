# Dockerfile for the processing service with FFMPEG
# This image includes FFMPEG with H.265/HEVC support for video transcoding

FROM debian:bookworm-slim

LABEL org.opencontainers.image.source="https://github.com/your-username/surtr-media"
LABEL org.opencontainers.image.description="MediaVault Processing Service with FFMPEG"
LABEL org.opencontainers.image.licenses="MIT"

# Install FFMPEG with libx265 support and other dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    libx265-dev \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

# Verify FFMPEG installation and H.265 support
RUN ffmpeg -version && \
    ffmpeg -encoders 2>/dev/null | grep libx265 && \
    echo "FFMPEG with libx265 successfully installed"

# Create a non-root user for security
RUN groupadd --gid 1000 appgroup && \
    useradd --uid 1000 --gid appgroup --shell /bin/bash --create-home appuser

# Create directories for temporary processing files
RUN mkdir -p /tmp/processing && \
    chown -R appuser:appgroup /tmp/processing

# Set working directory
WORKDIR /app

# Copy the application binary (provided during Encore build)
# The actual binary will be copied/mounted by Encore during deployment
COPY --chown=appuser:appgroup . .

# Switch to non-root user
USER appuser

# Set environment variables for FFMPEG
ENV FFMPEG_BINARY=/usr/bin/ffmpeg
ENV FFPROBE_BINARY=/usr/bin/ffprobe
ENV TEMP_DIR=/tmp/processing

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:4000/healthz || exit 1

# Entry point will be provided by Encore
CMD ["./processing"]


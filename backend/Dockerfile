# Multi-stage Dockerfile for MediaVault Backend
# Stage 1: Build the Encore application
FROM golang:1.24-bookworm AS builder

# Install Encore CLI
RUN curl -L https://encore.dev/install.sh | bash
ENV PATH="/root/.encore/bin:${PATH}"

# Set working directory
WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the entire backend source
COPY . .

# Build the Encore application
# Note: For self-hosted deployments, we use `encore build docker`
RUN encore build docker --base-image=debian:bookworm-slim mediavault

# Stage 2: Production runtime
FROM debian:bookworm-slim AS runtime

# Install runtime dependencies including FFMPEG for processing service
RUN apt-get update && apt-get install -y \
    ffmpeg \
    libx265-dev \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN useradd -m -s /bin/bash appuser

# Set working directory
WORKDIR /app

# Copy built application from builder
# Note: Adjust this path based on Encore's output
COPY --from=builder /app/.encore/build/docker/mediavault /app/

# Change ownership to non-root user
RUN chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose default Encore port
EXPOSE 4000

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:4000/healthz || exit 1

# Default command
CMD ["./mediavault"]


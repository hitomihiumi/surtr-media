# MediaVault Infrastructure & Deployment

## Overview

This document describes the infrastructure setup and deployment process for MediaVault.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Load Balancer                           │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    MediaVault Backend                           │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────────┐   │
│  │   Auth   │ │   Media  │ │Collection│ │    Processing    │   │
│  │ Service  │ │  Service │ │ Service  │ │     Service      │   │
│  └──────────┘ └──────────┘ └──────────┘ └──────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
        │               │            │               │
        ▼               ▼            ▼               ▼
┌──────────────┐  ┌──────────────┐  ┌─────────┐  ┌──────────┐
│  PostgreSQL  │  │    MinIO     │  │   NSQ   │  │  FFMPEG  │
│   Database   │  │  (S3 Store)  │  │ Pub/Sub │  │ (H.265)  │
└──────────────┘  └──────────────┘  └─────────┘  └──────────┘
```

## Docker Images

### Backend Image
- **Registry**: `ghcr.io/<owner>/surtr-media/mediavault-backend`
- **Tags**: `latest`, `main`, `<commit-sha>`

### Processing Image
- **Registry**: `ghcr.io/<owner>/surtr-media/mediavault-processing`
- **Tags**: `latest`, `main`, `<commit-sha>`
- **Includes**: FFMPEG with H.265/HEVC support

## CI/CD Pipeline

The GitHub Actions workflow (`.github/workflows/docker-build.yml`) automatically:

1. **Triggers on**:
   - Push to `main`/`master` branches (only backend changes)
   - Pull requests (build only, no push)
   - Manual workflow dispatch

2. **Builds**:
   - Multi-platform images (linux/amd64, linux/arm64)
   - Uses Docker layer caching for faster builds

3. **Pushes to**:
   - GitHub Container Registry (ghcr.io)
   - Automatic tagging with branch name, commit SHA, and `latest`

## Local Development

### Prerequisites
- Docker & Docker Compose
- Go 1.24+
- Encore CLI

### Quick Start

1. **Copy environment file**:
   ```bash
   cp .env.example .env
   # Edit .env with your Discord OAuth credentials
   ```

2. **Start infrastructure**:
   ```bash
   docker-compose up -d
   ```

3. **Run Encore locally** (for development):
   ```bash
   cd backend
   encore run
   ```

### Using Pre-built Images

```bash
# Pull and run the latest backend
docker-compose up -d
```

### Building Locally

```bash
docker-compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

## Production Deployment

### Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `POSTGRES_HOST` | PostgreSQL host:port | Yes |
| `POSTGRES_USER` | Database username | Yes |
| `POSTGRES_PASSWORD` | Database password | Yes |
| `MINIO_ROOT_USER` | MinIO access key | Yes |
| `MINIO_ROOT_PASSWORD` | MinIO secret key | Yes |
| `DISCORD_CLIENT_ID` | Discord OAuth client ID | Yes |
| `DISCORD_CLIENT_SECRET` | Discord OAuth secret | Yes |
| `SESSION_SECRET` | Session encryption key | Yes |
| `API_BASE_URL` | Public API URL | Yes |
| `FRONTEND_URL` | Frontend URL (for CORS) | Yes |

### Infrastructure Configuration

Encore infrastructure is configured via JSON files in `backend/infra/`:

- `local.infra.json` - Local development
- `production.infra.json` - Production deployment

### Self-Hosted Deployment

1. **Build Docker image**:
   ```bash
   cd backend
   encore build docker --base-image=debian:bookworm-slim mediavault
   ```

2. **Run with Docker Compose**:
   ```bash
   docker-compose -f docker-compose.yml up -d
   ```

3. **Or deploy to Kubernetes** using the generated image.

## Services

### Auth Service
- Discord OAuth2 authentication
- Session management
- Port: 4000 (shared gateway)

### Media Service
- File upload (presigned URLs)
- Media metadata management
- Tagging system
- Port: 4000 (shared gateway)

### Collection Service
- Group media into collections
- Sharing with tokens
- Port: 4000 (shared gateway)

### Processing Service
- Async video transcoding
- H.265/HEVC conversion
- Pub/Sub worker (NSQ)

## Monitoring

### Health Checks
- All services expose `/healthz` endpoint
- Docker health checks configured

### Metrics
- Prometheus metrics available at `/metrics`
- Configure `metrics.remote_write` in infra config for remote collection

## Security Considerations

1. **Never commit `.env` files** - Use `.env.example` as template
2. **Rotate secrets regularly** - Especially `SESSION_SECRET`
3. **Use TLS in production** - Configure `S3_USE_SSL=true`
4. **Network isolation** - Services communicate via internal Docker network


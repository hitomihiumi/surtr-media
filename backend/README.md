# MediaVault Backend

A self-hosted media storage and streaming service built with [Encore.go](https://encore.dev).

## Features

- 🔐 **Discord OAuth2 Authentication** - Secure login via Discord
- 📁 **Media Storage** - Upload and organize media files with tags
- 📂 **Collections** - Group media into collections with sharing capabilities
- 🎬 **Video Transcoding** - Automatic H.265/HEVC transcoding for efficient storage
- 🔗 **Presigned URLs** - Secure direct uploads and streaming

## Architecture

```
/backend
  /auth        # Discord OAuth & Session management
  /media       # Media metadata, tagging, presigned URLs
  /collection  # Grouping media, sharing logic
  /processing  # Async FFMPEG transcoding (H.265)
```

## Prerequisites

- [Go 1.21+](https://golang.org/dl/)
- [Encore CLI](https://encore.dev/docs/install)
- [Docker](https://www.docker.com/) (for MinIO and optional PostgreSQL)
- [FFMPEG](https://ffmpeg.org/) with libx265 support (for local video processing)

## Getting Started

### 1. Install Encore CLI

```bash
# macOS
brew install encoredev/tap/encore

# Windows (using scoop)
scoop bucket add encore https://github.com/encoredev/scoop-bucket.git
scoop install encore

# Linux
curl -L https://encore.dev/install.sh | bash
```

### 2. Start Infrastructure

```bash
# From the root directory
docker-compose up -d
```

This starts:
- MinIO (S3-compatible storage) on ports 9000 (API) and 9001 (Console)
- PostgreSQL on port 5432

### 3. Configure Environment

Copy the example environment file and fill in your Discord OAuth credentials:

```bash
cd backend
cp .env.example .env
# Edit .env with your Discord OAuth credentials
```

To get Discord OAuth credentials:
1. Go to [Discord Developer Portal](https://discord.com/developers/applications)
2. Create a new application
3. Go to OAuth2 > General
4. Copy Client ID and Client Secret
5. Add redirect URI: `http://localhost:4000/auth/discord/callback`

### 4. Run the Backend

```bash
cd backend
encore run
```

The API will be available at `http://localhost:4000`.

## API Endpoints

### Authentication

| Method | Path | Description |
|--------|------|-------------|
| GET | `/auth/discord/login` | Get Discord OAuth login URL |
| GET | `/auth/discord/callback` | OAuth callback handler |
| POST | `/auth/logout` | Logout (requires auth) |
| GET | `/auth/me` | Get current user (requires auth) |

### Media

| Method | Path | Description |
|--------|------|-------------|
| POST | `/media/upload/sign` | Get presigned upload URL |
| POST | `/media/upload/confirm` | Confirm upload complete |
| GET | `/media` | List user's media |
| GET | `/media/:id` | Get media details |
| PATCH | `/media/:id/tags` | Update media tags |
| DELETE | `/media/:id` | Delete media |

### Collections

| Method | Path | Description |
|--------|------|-------------|
| POST | `/collection` | Create collection |
| GET | `/collection` | List user's collections |
| GET | `/collection/:id` | Get collection (with sharing) |
| PATCH | `/collection/:id` | Update collection |
| DELETE | `/collection/:id` | Delete collection |
| POST | `/collection/:id/add` | Add media to collection |
| DELETE | `/collection/:id/media/:mediaID` | Remove media from collection |
| PUT | `/collection/:id/share` | Update sharing settings |

### Processing

| Method | Path | Description |
|--------|------|-------------|
| GET | `/processing/:mediaID/status` | Get processing status |

## Usage Examples

### Upload a Video

```javascript
// 1. Get presigned upload URL
const signResponse = await fetch('/media/upload/sign', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${token}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    filename: 'video.mp4',
    mime_type: 'video/mp4'
  })
});
const { upload_url, media_id } = await signResponse.json();

// 2. Upload file directly to S3
await fetch(upload_url, {
  method: 'PUT',
  body: file,
  headers: { 'Content-Type': 'video/mp4' }
});

// 3. Confirm upload
await fetch('/media/upload/confirm', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${token}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    media_id,
    title: 'My Video',
    size_bytes: file.size
  })
});
```

### Create and Share a Collection

```javascript
// Create collection
const collection = await fetch('/collection', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${token}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    title: 'My Collection',
    description: 'A collection of videos'
  })
}).then(r => r.json());

// Add media
await fetch(`/collection/${collection.id}/add`, {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${token}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({ media_id: 'some-media-uuid' })
});

// Make public or get share link
const shareSettings = await fetch(`/collection/${collection.id}/share`, {
  method: 'PUT',
  headers: {
    'Authorization': `Bearer ${token}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({ is_public: true })
}).then(r => r.json());
```

## Video Processing

Videos are automatically transcoded to H.265/HEVC format when uploaded:

- **Codec:** libx265 (HEVC)
- **CRF:** 28 (good quality/size balance)
- **Preset:** fast
- **Audio:** AAC
- **Fast start:** Enabled for streaming

The processing service requires FFMPEG with libx265 support. The included Dockerfile provides a runtime with all necessary dependencies.

## Development

### View Encore Dashboard

```bash
encore run
# Open http://localhost:9400 in your browser
```

### Run Tests

```bash
encore test ./...
```

### Generate API Docs

Encore automatically generates API documentation. Access it at `http://localhost:9400/api` when the server is running.

## License

MIT


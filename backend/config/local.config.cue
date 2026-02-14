# Encore Configuration File
# This file contains environment-specific configuration values

# S3/MinIO Configuration
S3Endpoint: "localhost:9000"
S3AccessKey: "minioadmin"
S3SecretKey: "minioadmin"
S3Bucket: "media-vault"
S3UseSSL: false

# Discord OAuth Configuration
DiscordClientID: ""
DiscordClientSecret: ""
DiscordRedirectURL: "http://localhost:4000/auth/discord/callback"

# Session Configuration
SessionSecret: "development-secret-change-in-production"

# API Configuration
APIBaseURL: "http://localhost:4000"
FrontendURL: "http://localhost:3000"


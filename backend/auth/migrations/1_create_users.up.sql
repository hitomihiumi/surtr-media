-- Create users table
CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    discord_id TEXT UNIQUE NOT NULL,
    username TEXT NOT NULL,
    avatar_url TEXT,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Index for Discord ID lookups
CREATE INDEX idx_users_discord_id ON users(discord_id);


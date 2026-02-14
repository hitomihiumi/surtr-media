// Package auth handles Discord OAuth2 authentication and session management.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
	"encore.dev/config"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
)

// Config for Discord OAuth2
var cfg struct {
	DiscordClientID     config.String
	DiscordClientSecret config.String
	DiscordRedirectURI  config.String
	FrontendURL         config.String
	SessionSecret       config.String
}

// Database for users
var db = sqldb.NewDatabase("auth", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// DiscordUser represents the Discord user data from OAuth
type DiscordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
}

// Session represents a user session
type Session struct {
	ID        string
	UserID    int64
	ExpiresAt time.Time
}

// UserData represents the authenticated user context
type UserData struct {
	UserID    int64
	DiscordID string
	Username  string
}

// sessions stores active sessions in memory (in production, use Redis)
var sessions = make(map[string]*Session)

// LoginResponse contains the Discord OAuth login URL
type LoginResponse struct {
	URL string `json:"url"`
}

// Login redirects to Discord OAuth URL
//
//encore:api public method=GET path=/auth/discord/login
func Login(ctx context.Context) (*LoginResponse, error) {
	state := generateRandomState()

	params := url.Values{
		"client_id":     {cfg.DiscordClientID()},
		"redirect_uri":  {cfg.DiscordRedirectURI()},
		"response_type": {"code"},
		"scope":         {"identify"},
		"state":         {state},
	}

	authURL := fmt.Sprintf("https://discord.com/api/oauth2/authorize?%s", params.Encode())

	return &LoginResponse{URL: authURL}, nil
}

// CallbackRequest contains the OAuth callback parameters
type CallbackRequest struct {
	Code  string `query:"code"`
	State string `query:"state"`
}

// CallbackResponse contains the session token after successful auth
type CallbackResponse struct {
	Token       string `json:"token"`
	RedirectURL string `json:"redirect_url"`
}

// Callback handles the Discord OAuth callback
//
//encore:api public method=GET path=/auth/discord/callback
func Callback(ctx context.Context, req *CallbackRequest) (*CallbackResponse, error) {
	if req.Code == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("missing authorization code").Err()
	}

	// Exchange code for token
	tokenData, err := exchangeCodeForToken(ctx, req.Code)
	if err != nil {
		rlog.Error("failed to exchange code for token", "error", err)
		return nil, errs.B().Code(errs.Internal).Msg("failed to authenticate with Discord").Err()
	}

	// Get user info from Discord
	discordUser, err := getDiscordUser(ctx, tokenData.AccessToken)
	if err != nil {
		rlog.Error("failed to get Discord user", "error", err)
		return nil, errs.B().Code(errs.Internal).Msg("failed to get user info from Discord").Err()
	}

	// Upsert user in database
	user, err := upsertUser(ctx, discordUser)
	if err != nil {
		rlog.Error("failed to upsert user", "error", err)
		return nil, errs.B().Code(errs.Internal).Msg("failed to create user").Err()
	}

	// Create session
	sessionToken := generateSessionToken()
	session := &Session{
		ID:        sessionToken,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days
	}
	sessions[sessionToken] = session

	return &CallbackResponse{
		Token:       sessionToken,
		RedirectURL: cfg.FrontendURL(),
	}, nil
}

// LogoutResponse confirms logout
type LogoutResponse struct {
	Success bool `json:"success"`
}

// Logout clears the user session
//
//encore:api auth method=POST path=/auth/logout
func Logout(ctx context.Context) (*LogoutResponse, error) {
	userData := auth.Data().(*UserData)

	// Find and delete session for this user
	for token, session := range sessions {
		if session.UserID == userData.UserID {
			delete(sessions, token)
		}
	}

	return &LogoutResponse{Success: true}, nil
}

// MeResponse returns current user info
type MeResponse struct {
	ID        int64  `json:"id"`
	DiscordID string `json:"discord_id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
}

// Me returns the current authenticated user
//
//encore:api auth method=GET path=/auth/me
func Me(ctx context.Context) (*MeResponse, error) {
	userData := auth.Data().(*UserData)

	var user MeResponse
	err := db.QueryRow(ctx, `
		SELECT id, discord_id, username, COALESCE(avatar_url, '') as avatar_url
		FROM users WHERE id = $1
	`, userData.UserID).Scan(&user.ID, &user.DiscordID, &user.Username, &user.AvatarURL)

	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("user not found").Err()
	}

	return &user, nil
}

// User represents a user in the database
type User struct {
	ID        int64
	DiscordID string
	Username  string
	AvatarURL string
}

func upsertUser(ctx context.Context, discordUser *DiscordUser) (*User, error) {
	avatarURL := ""
	if discordUser.Avatar != "" {
		avatarURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", discordUser.ID, discordUser.Avatar)
	}

	var user User
	err := db.QueryRow(ctx, `
		INSERT INTO users (discord_id, username, avatar_url, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (discord_id) DO UPDATE SET
			username = EXCLUDED.username,
			avatar_url = EXCLUDED.avatar_url
		RETURNING id, discord_id, username, COALESCE(avatar_url, '')
	`, discordUser.ID, discordUser.Username, avatarURL).Scan(&user.ID, &user.DiscordID, &user.Username, &user.AvatarURL)

	if err != nil {
		return nil, err
	}

	return &user, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func exchangeCodeForToken(ctx context.Context, code string) (*tokenResponse, error) {
	data := url.Values{
		"client_id":     {cfg.DiscordClientID()},
		"client_secret": {cfg.DiscordClientSecret()},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.DiscordRedirectURI()},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://discord.com/api/oauth2/token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("failed to exchange code for token")
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	return &tokenResp, nil
}

func getDiscordUser(ctx context.Context, accessToken string) (*DiscordUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://discord.com/api/users/@me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("failed to get Discord user")
	}

	var user DiscordUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}

	return &user, nil
}

func generateRandomState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func generateSessionToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

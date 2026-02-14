package auth

import (
	"context"
	"time"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
)

// AuthParams contains the authorization header token
type AuthParams struct {
	Authorization string `header:"Authorization"`
}

// AuthHandler validates the session token and returns user data
//
//encore:authhandler
func AuthHandler(ctx context.Context, params *AuthParams) (auth.UID, *UserData, error) {
	token := params.Authorization

	// Remove "Bearer " prefix if present
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	if token == "" {
		return "", nil, errs.B().Code(errs.Unauthenticated).Msg("missing authorization token").Err()
	}

	// Look up session
	session, exists := sessions[token]
	if !exists {
		return "", nil, errs.B().Code(errs.Unauthenticated).Msg("invalid session").Err()
	}

	// Check expiration
	if time.Now().After(session.ExpiresAt) {
		delete(sessions, token)
		return "", nil, errs.B().Code(errs.Unauthenticated).Msg("session expired").Err()
	}

	// Get user from database
	var userData UserData
	err := db.QueryRow(ctx, `
		SELECT id, discord_id, username
		FROM users WHERE id = $1
	`, session.UserID).Scan(&userData.UserID, &userData.DiscordID, &userData.Username)

	if err != nil {
		return "", nil, errs.B().Code(errs.Unauthenticated).Msg("user not found").Err()
	}

	return auth.UID(userData.DiscordID), &userData, nil
}

// Package collection handles media grouping and sharing logic.
package collection

import (
	"context"
	"os"
	"time"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
	"encore.dev/storage/sqldb"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	authpkg "encore.app/auth"
)

// Secrets for S3/MinIO (for generating stream URLs)
var secrets struct {
	S3AccessKey string
	S3SecretKey string
}

// getS3Endpoint returns the S3 endpoint
func getS3Endpoint() string {
	if val := os.Getenv("S3_ENDPOINT"); val != "" {
		return val
	}
	return "localhost:9000"
}

// getS3Bucket returns the S3 bucket name
func getS3Bucket() string {
	if val := os.Getenv("S3_BUCKET"); val != "" {
		return val
	}
	return "media-vault"
}

// getS3UseSSL returns whether to use SSL for S3
func getS3UseSSL() bool {
	return os.Getenv("S3_USE_SSL") == "true"
}

// Database for collections
var db = sqldb.NewDatabase("collection", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// MediaDatabase for querying media
var mediaDB = sqldb.Named("media")

// getMinioClient creates a MinIO client
func getMinioClient() (*minio.Client, error) {
	return minio.New(getS3Endpoint(), &minio.Options{
		Creds:  credentials.NewStaticV4(secrets.S3AccessKey, secrets.S3SecretKey, ""),
		Secure: getS3UseSSL(),
	})
}

// CreateCollectionRequest contains data for creating a collection
type CreateCollectionRequest struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// CollectionResponse represents a collection
type CollectionResponse struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	IsPublic    bool      `json:"is_public"`
	ShareToken  string    `json:"share_token"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateCollection creates a new collection
//
//encore:api auth method=POST path=/collection
func CreateCollection(ctx context.Context, req *CreateCollectionRequest) (*CollectionResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	if req.Title == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("title is required").Err()
	}

	var resp CollectionResponse
	err := db.QueryRow(ctx, `
		INSERT INTO collections (owner_id, title, description, created_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING id, title, COALESCE(description, ''), is_public, share_token, created_at
	`, userData.UserID, req.Title, req.Description).Scan(
		&resp.ID, &resp.Title, &resp.Description, &resp.IsPublic, &resp.ShareToken, &resp.CreatedAt)

	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to create collection").Err()
	}

	return &resp, nil
}

// AddMediaRequest contains media to add to a collection
type AddMediaRequest struct {
	MediaID string `json:"media_id"`
}

// AddMediaResponse confirms the addition
type AddMediaResponse struct {
	Success bool `json:"success"`
}

// AddMedia adds a media item to a collection
//
//encore:api auth method=POST path=/collection/:id/add
func AddMedia(ctx context.Context, id string, req *AddMediaRequest) (*AddMediaResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	// Verify collection ownership
	var ownerID int64
	err := db.QueryRow(ctx, `SELECT owner_id FROM collections WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("collection not found").Err()
	}
	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Verify media ownership
	var mediaOwnerID int64
	err = mediaDB.QueryRow(ctx, `SELECT owner_id FROM media WHERE id = $1`, req.MediaID).Scan(&mediaOwnerID)
	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("media not found").Err()
	}
	if mediaOwnerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized to add this media").Err()
	}

	// Add media to collection
	_, err = db.Exec(ctx, `
		INSERT INTO collection_items (collection_id, media_id, added_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT DO NOTHING
	`, id, req.MediaID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to add media to collection").Err()
	}

	return &AddMediaResponse{Success: true}, nil
}

// RemoveMediaRequest contains media to remove from a collection
type RemoveMediaRequest struct {
	MediaID string `json:"media_id"`
}

// RemoveMediaResponse confirms the removal
type RemoveMediaResponse struct {
	Success bool `json:"success"`
}

// RemoveMedia removes a media item from a collection
//
//encore:api auth method=DELETE path=/collection/:id/media/:mediaID
func RemoveMedia(ctx context.Context, id string, mediaID string) (*RemoveMediaResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	// Verify collection ownership
	var ownerID int64
	err := db.QueryRow(ctx, `SELECT owner_id FROM collections WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("collection not found").Err()
	}
	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Remove media from collection
	_, err = db.Exec(ctx, `
		DELETE FROM collection_items WHERE collection_id = $1 AND media_id = $2
	`, id, mediaID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to remove media from collection").Err()
	}

	return &RemoveMediaResponse{Success: true}, nil
}

// UpdateShareRequest contains sharing options
type UpdateShareRequest struct {
	IsPublic        *bool `json:"is_public,omitempty"`
	RegenerateToken bool  `json:"regenerate_token,omitempty"`
}

// UpdateShareResponse contains the updated share settings
type UpdateShareResponse struct {
	IsPublic   bool   `json:"is_public"`
	ShareToken string `json:"share_token"`
	ShareURL   string `json:"share_url"`
}

// UpdateShare updates sharing settings for a collection
//
//encore:api auth method=PUT path=/collection/:id/share
func UpdateShare(ctx context.Context, id string, req *UpdateShareRequest) (*UpdateShareResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	// Verify collection ownership
	var ownerID int64
	var currentIsPublic bool
	var currentToken string
	err := db.QueryRow(ctx, `
		SELECT owner_id, is_public, share_token 
		FROM collections WHERE id = $1
	`, id).Scan(&ownerID, &currentIsPublic, &currentToken)
	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("collection not found").Err()
	}
	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Update settings
	newIsPublic := currentIsPublic
	newToken := currentToken

	if req.IsPublic != nil {
		newIsPublic = *req.IsPublic
	}
	if req.RegenerateToken {
		newToken = uuid.New().String()
	}

	_, err = db.Exec(ctx, `
		UPDATE collections SET is_public = $2, share_token = $3 WHERE id = $1
	`, id, newIsPublic, newToken)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to update share settings").Err()
	}

	return &UpdateShareResponse{
		IsPublic:   newIsPublic,
		ShareToken: newToken,
		ShareURL:   "/collection/" + id + "?token=" + newToken,
	}, nil
}

// CollectionMediaItem represents a media item in a collection
type CollectionMediaItem struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	OriginalFilename string    `json:"original_filename"`
	MimeType         string    `json:"mime_type"`
	Status           string    `json:"status"`
	StreamURL        string    `json:"stream_url,omitempty"`
	AddedAt          time.Time `json:"added_at"`
}

// GetCollectionRequest contains the optional token for access
type GetCollectionRequest struct {
	Token string `query:"token"`
}

// GetCollectionResponse contains collection details and items
type GetCollectionResponse struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	IsPublic    bool                  `json:"is_public"`
	IsOwner     bool                  `json:"is_owner"`
	ItemCount   int                   `json:"item_count"`
	Items       []CollectionMediaItem `json:"items"`
	CreatedAt   time.Time             `json:"created_at"`
}

// GetCollection fetches collection details with access control
//
//encore:api public method=GET path=/collection/:id
func GetCollection(ctx context.Context, id string, req *GetCollectionRequest) (*GetCollectionResponse, error) {
	// Get collection
	var resp GetCollectionResponse
	var ownerID int64
	var shareToken string

	err := db.QueryRow(ctx, `
		SELECT id, owner_id, title, COALESCE(description, ''), is_public, share_token, created_at
		FROM collections WHERE id = $1
	`, id).Scan(&resp.ID, &ownerID, &resp.Title, &resp.Description, &resp.IsPublic, &shareToken, &resp.CreatedAt)

	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("collection not found").Err()
	}

	// Check access permissions
	var userID int64
	if userData, ok := auth.Data().(*authpkg.UserData); ok && userData != nil {
		userID = userData.UserID
	}

	resp.IsOwner = userID == ownerID

	// Security Rules:
	// 1. Allow if requester is owner
	// 2. Allow if collection is public
	// 3. Allow if token matches share_token
	// 4. Else: 403 Forbidden
	hasAccess := resp.IsOwner || resp.IsPublic || (req.Token != "" && req.Token == shareToken)

	if !hasAccess {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied").Err()
	}

	// Get collection items
	rows, err := db.Query(ctx, `
		SELECT media_id, added_at FROM collection_items 
		WHERE collection_id = $1 
		ORDER BY added_at DESC
	`, id)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to get collection items").Err()
	}
	defer rows.Close()

	var items []CollectionMediaItem
	client, _ := getMinioClient()

	for rows.Next() {
		var mediaID string
		var addedAt time.Time
		if err := rows.Scan(&mediaID, &addedAt); err != nil {
			continue
		}

		// Get media details
		var item CollectionMediaItem
		var s3KeyOriginal, s3KeyProcessed string
		err = mediaDB.QueryRow(ctx, `
			SELECT id, COALESCE(title, ''), COALESCE(original_filename, ''), 
				   COALESCE(mime_type, ''), status,
				   s3_key_original, COALESCE(s3_key_processed, '')
			FROM media WHERE id = $1
		`, mediaID).Scan(&item.ID, &item.Title, &item.OriginalFilename,
			&item.MimeType, &item.Status, &s3KeyOriginal, &s3KeyProcessed)

		if err != nil {
			continue
		}

		item.AddedAt = addedAt

		// Generate stream URL if ready
		if item.Status == "ready" && client != nil {
			s3Key := s3KeyProcessed
			if s3Key == "" {
				s3Key = s3KeyOriginal
			}
			streamURL, err := client.PresignedGetObject(ctx, getS3Bucket(), s3Key, 4*time.Hour, nil)
			if err == nil {
				item.StreamURL = streamURL.String()
			}
		}

		items = append(items, item)
	}

	if items == nil {
		items = []CollectionMediaItem{}
	}

	resp.Items = items
	resp.ItemCount = len(items)

	return &resp, nil
}

// ListCollectionsResponse contains the user's collections
type ListCollectionsResponse struct {
	Collections []CollectionResponse `json:"collections"`
}

// ListCollections returns all collections for the authenticated user
//
//encore:api auth method=GET path=/collection
func ListCollections(ctx context.Context) (*ListCollectionsResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	rows, err := db.Query(ctx, `
		SELECT id, title, COALESCE(description, ''), is_public, share_token, created_at
		FROM collections 
		WHERE owner_id = $1
		ORDER BY created_at DESC
	`, userData.UserID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to list collections").Err()
	}
	defer rows.Close()

	var collections []CollectionResponse
	for rows.Next() {
		var c CollectionResponse
		if err := rows.Scan(&c.ID, &c.Title, &c.Description, &c.IsPublic, &c.ShareToken, &c.CreatedAt); err != nil {
			continue
		}
		collections = append(collections, c)
	}

	if collections == nil {
		collections = []CollectionResponse{}
	}

	return &ListCollectionsResponse{Collections: collections}, nil
}

// DeleteCollectionResponse confirms deletion
type DeleteCollectionResponse struct {
	Success bool `json:"success"`
}

// DeleteCollection deletes a collection
//
//encore:api auth method=DELETE path=/collection/:id
func DeleteCollection(ctx context.Context, id string) (*DeleteCollectionResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	// Verify ownership
	var ownerID int64
	err := db.QueryRow(ctx, `SELECT owner_id FROM collections WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("collection not found").Err()
	}
	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Delete collection (cascade will remove collection_items)
	_, err = db.Exec(ctx, `DELETE FROM collections WHERE id = $1`, id)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to delete collection").Err()
	}

	return &DeleteCollectionResponse{Success: true}, nil
}

// UpdateCollectionRequest contains data to update a collection
type UpdateCollectionRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}

// UpdateCollection updates collection details
//
//encore:api auth method=PATCH path=/collection/:id
func UpdateCollection(ctx context.Context, id string, req *UpdateCollectionRequest) (*CollectionResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	// Verify ownership
	var ownerID int64
	err := db.QueryRow(ctx, `SELECT owner_id FROM collections WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("collection not found").Err()
	}
	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Update collection
	var resp CollectionResponse
	err = db.QueryRow(ctx, `
		UPDATE collections 
		SET title = COALESCE($2, title),
			description = COALESCE($3, description)
		WHERE id = $1
		RETURNING id, title, COALESCE(description, ''), is_public, share_token, created_at
	`, id, req.Title, req.Description).Scan(
		&resp.ID, &resp.Title, &resp.Description, &resp.IsPublic, &resp.ShareToken, &resp.CreatedAt)

	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to update collection").Err()
	}

	return &resp, nil
}

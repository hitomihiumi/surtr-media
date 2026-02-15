// Package media handles media metadata, tagging, and S3 presigned URLs.
package media

import (
	"context"
	"fmt"
	"os"
	"time"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
	"encore.dev/pubsub"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	authpkg "encore.app/auth"
)

// Secrets for S3/MinIO
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

// Database for media
var db = sqldb.NewDatabase("media", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// MediaUploaded is published when a media upload is confirmed
type MediaUploaded struct {
	MediaID string `json:"media_id"`
	S3Key   string `json:"s3_key"`
	OwnerID int64  `json:"owner_id"`
}

// MediaUploadedTopic is the Pub/Sub topic for media uploads
var MediaUploadedTopic = pubsub.NewTopic[*MediaUploaded]("media-uploaded", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})

// getMinioClient creates a MinIO client
func getMinioClient() (*minio.Client, error) {
	return minio.New(getS3Endpoint(), &minio.Options{
		Creds:  credentials.NewStaticV4(secrets.S3AccessKey, secrets.S3SecretKey, ""),
		Secure: getS3UseSSL(),
	})
}

// SignUploadRequest contains parameters for generating a presigned upload URL
type SignUploadRequest struct {
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
}

// SignUploadResponse contains the presigned URL and S3 key
type SignUploadResponse struct {
	UploadURL string `json:"upload_url"`
	S3Key     string `json:"s3_key"`
	MediaID   string `json:"media_id"`
}

// SignUpload generates a presigned PUT URL for direct upload to S3
//
//encore:api auth method=POST path=/media/upload/sign
func SignUpload(ctx context.Context, req *SignUploadRequest) (*SignUploadResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	if req.Filename == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("filename is required").Err()
	}

	// Generate unique S3 key
	mediaID := uuid.New().String()
	s3Key := fmt.Sprintf("original/%d/%s/%s", userData.UserID, mediaID, req.Filename)

	// Get MinIO client
	client, err := getMinioClient()
	if err != nil {
		rlog.Error("failed to create MinIO client", "error", err)
		return nil, errs.B().Code(errs.Internal).Msg("failed to create storage client").Err()
	}

	// Generate presigned URL (valid for 15 minutes)
	presignedURL, err := client.PresignedPutObject(ctx, getS3Bucket(), s3Key, 15*time.Minute)
	if err != nil {
		rlog.Error("failed to generate presigned URL", "error", err)
		return nil, errs.B().Code(errs.Internal).Msg("failed to generate upload URL").Err()
	}

	// Create media record with 'uploading' status
	_, err = db.Exec(ctx, `
		INSERT INTO media (id, owner_id, original_filename, s3_key_original, mime_type, status, created_at)
		VALUES ($1, $2, $3, $4, $5, 'uploading', NOW())
	`, mediaID, userData.UserID, req.Filename, s3Key, req.MimeType)

	if err != nil {
		rlog.Error("failed to create media record", "error", err)
		return nil, errs.B().Code(errs.Internal).Msg("failed to create media record").Err()
	}

	return &SignUploadResponse{
		UploadURL: presignedURL.String(),
		S3Key:     s3Key,
		MediaID:   mediaID,
	}, nil
}

// ConfirmUploadRequest contains the media ID to confirm upload
type ConfirmUploadRequest struct {
	MediaID   string `json:"media_id"`
	Title     string `json:"title,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// ConfirmUploadResponse confirms the upload was processed
type ConfirmUploadResponse struct {
	MediaID string `json:"media_id"`
	Status  string `json:"status"`
}

// ConfirmUpload notifies the backend that an upload is complete
//
//encore:api auth method=POST path=/media/upload/confirm
func ConfirmUpload(ctx context.Context, req *ConfirmUploadRequest) (*ConfirmUploadResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	if req.MediaID == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("media_id is required").Err()
	}

	// Verify ownership and get S3 key
	var s3Key string
	var ownerID int64
	err := db.QueryRow(ctx, `
		SELECT s3_key_original, owner_id FROM media WHERE id = $1
	`, req.MediaID).Scan(&s3Key, &ownerID)

	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("media not found").Err()
	}

	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Update status to 'queued' and optionally update title/size
	_, err = db.Exec(ctx, `
		UPDATE media 
		SET status = 'queued',
			title = COALESCE(NULLIF($2, ''), title),
			size_bytes = COALESCE(NULLIF($3, 0), size_bytes)
		WHERE id = $1
	`, req.MediaID, req.Title, req.SizeBytes)

	if err != nil {
		rlog.Error("failed to update media status", "error", err)
		return nil, errs.B().Code(errs.Internal).Msg("failed to update media").Err()
	}

	// Publish event to processing topic
	_, err = MediaUploadedTopic.Publish(ctx, &MediaUploaded{
		MediaID: req.MediaID,
		S3Key:   s3Key,
		OwnerID: ownerID,
	})

	if err != nil {
		rlog.Error("failed to publish media uploaded event", "error", err)
		// Don't fail the request, processing can be retried
	}

	return &ConfirmUploadResponse{
		MediaID: req.MediaID,
		Status:  "queued",
	}, nil
}

// UpdateTagsRequest contains tags to add or remove
type UpdateTagsRequest struct {
	AddTags    []string `json:"add_tags,omitempty"`
	RemoveTags []string `json:"remove_tags,omitempty"`
}

// UpdateTagsResponse confirms the tag update
type UpdateTagsResponse struct {
	MediaID string   `json:"media_id"`
	Tags    []string `json:"tags"`
}

// UpdateTags adds or removes tags for a media item
//
//encore:api auth method=PATCH path=/media/:id/tags
func UpdateTags(ctx context.Context, id string, req *UpdateTagsRequest) (*UpdateTagsResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	// Verify ownership
	var ownerID int64
	err := db.QueryRow(ctx, `SELECT owner_id FROM media WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("media not found").Err()
	}
	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Add tags
	for _, tagName := range req.AddTags {
		// Upsert tag
		var tagID int64
		err := db.QueryRow(ctx, `
			INSERT INTO tags (name) VALUES ($1)
			ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
			RETURNING id
		`, tagName).Scan(&tagID)
		if err != nil {
			continue
		}

		// Link tag to media
		_, _ = db.Exec(ctx, `
			INSERT INTO media_tags (media_id, tag_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, id, tagID)
	}

	// Remove tags
	for _, tagName := range req.RemoveTags {
		_, _ = db.Exec(ctx, `
			DELETE FROM media_tags 
			WHERE media_id = $1 AND tag_id = (SELECT id FROM tags WHERE name = $2)
		`, id, tagName)
	}

	// Get current tags
	rows, err := db.Query(ctx, `
		SELECT t.name FROM tags t
		JOIN media_tags mt ON t.id = mt.tag_id
		WHERE mt.media_id = $1
	`, id)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to get tags").Err()
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tags = append(tags, name)
		}
	}

	return &UpdateTagsResponse{
		MediaID: id,
		Tags:    tags,
	}, nil
}

// ListMediaRequest contains pagination and filter parameters
type ListMediaRequest struct {
	Page     int      `query:"page"`
	PageSize int      `query:"page_size"`
	Tags     []string `query:"tags"`
	Status   string   `query:"status"`
}

// MediaItem represents a media item in the list
type MediaItem struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	OriginalFilename string    `json:"original_filename"`
	MimeType         string    `json:"mime_type"`
	SizeBytes        int64     `json:"size_bytes"`
	DurationSeconds  int       `json:"duration_seconds"`
	Status           string    `json:"status"`
	Tags             []string  `json:"tags"`
	CreatedAt        time.Time `json:"created_at"`
}

// ListMediaResponse contains paginated media items
type ListMediaResponse struct {
	Items      []MediaItem `json:"items"`
	TotalCount int         `json:"total_count"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
}

// ListMedia lists the user's media with pagination and filtering
//
//encore:api auth method=GET path=/media
func ListMedia(ctx context.Context, req *ListMediaRequest) (*ListMediaResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	// Set defaults
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	// Build query
	query := `
		SELECT DISTINCT m.id, m.title, m.original_filename, m.mime_type, 
			   COALESCE(m.size_bytes, 0), COALESCE(m.duration_seconds, 0), 
			   m.status, m.created_at
		FROM media m
		LEFT JOIN media_tags mt ON m.id = mt.media_id
		LEFT JOIN tags t ON mt.tag_id = t.id
		WHERE m.owner_id = $1
	`
	countQuery := `
		SELECT COUNT(DISTINCT m.id)
		FROM media m
		LEFT JOIN media_tags mt ON m.id = mt.media_id
		LEFT JOIN tags t ON mt.tag_id = t.id
		WHERE m.owner_id = $1
	`

	args := []interface{}{userData.UserID}
	argIndex := 2

	if req.Status != "" {
		query += fmt.Sprintf(" AND m.status = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND m.status = $%d", argIndex)
		args = append(args, req.Status)
		argIndex++
	}

	if len(req.Tags) > 0 {
		query += fmt.Sprintf(" AND t.name = ANY($%d)", argIndex)
		countQuery += fmt.Sprintf(" AND t.name = ANY($%d)", argIndex)
		args = append(args, req.Tags)
		argIndex++
	}

	// Get total count
	var totalCount int
	countArgs := args
	if err := db.QueryRow(ctx, countQuery, countArgs...).Scan(&totalCount); err != nil {
		totalCount = 0
	}

	// Add pagination
	query += " ORDER BY m.created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIndex, argIndex+1)
	args = append(args, pageSize, offset)

	// Execute query
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		rlog.Error("failed to query media", "error", err)
		return nil, errs.B().Code(errs.Internal).Msg("failed to list media").Err()
	}
	defer rows.Close()

	var items []MediaItem
	for rows.Next() {
		var item MediaItem
		if err := rows.Scan(&item.ID, &item.Title, &item.OriginalFilename, &item.MimeType,
			&item.SizeBytes, &item.DurationSeconds, &item.Status, &item.CreatedAt); err != nil {
			continue
		}

		// Get tags for this media
		tagRows, err := db.Query(ctx, `
			SELECT t.name FROM tags t
			JOIN media_tags mt ON t.id = mt.tag_id
			WHERE mt.media_id = $1
		`, item.ID)
		if err == nil {
			for tagRows.Next() {
				var tagName string
				if err := tagRows.Scan(&tagName); err == nil {
					item.Tags = append(item.Tags, tagName)
				}
			}
			tagRows.Close()
		}

		items = append(items, item)
	}

	if items == nil {
		items = []MediaItem{}
	}

	return &ListMediaResponse{
		Items:      items,
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
	}, nil
}

// GetMediaRequest is empty as ID comes from path
type GetMediaResponse struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	OriginalFilename string    `json:"original_filename"`
	MimeType         string    `json:"mime_type"`
	SizeBytes        int64     `json:"size_bytes"`
	DurationSeconds  int       `json:"duration_seconds"`
	Status           string    `json:"status"`
	Tags             []string  `json:"tags"`
	StreamURL        string    `json:"stream_url,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// GetMedia returns details for a specific media item including stream URL
//
//encore:api auth method=GET path=/media/:id
func GetMedia(ctx context.Context, id string) (*GetMediaResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	var resp GetMediaResponse
	var s3KeyOriginal, s3KeyProcessed string
	var ownerID int64

	err := db.QueryRow(ctx, `
		SELECT id, COALESCE(title, ''), COALESCE(original_filename, ''), COALESCE(mime_type, ''),
			   COALESCE(size_bytes, 0), COALESCE(duration_seconds, 0), status, created_at,
			   owner_id, s3_key_original, COALESCE(s3_key_processed, '')
		FROM media WHERE id = $1
	`, id).Scan(&resp.ID, &resp.Title, &resp.OriginalFilename, &resp.MimeType,
		&resp.SizeBytes, &resp.DurationSeconds, &resp.Status, &resp.CreatedAt,
		&ownerID, &s3KeyOriginal, &s3KeyProcessed)

	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("media not found").Err()
	}

	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Get tags
	tagRows, err := db.Query(ctx, `
		SELECT t.name FROM tags t
		JOIN media_tags mt ON t.id = mt.tag_id
		WHERE mt.media_id = $1
	`, id)
	if err == nil {
		for tagRows.Next() {
			var tagName string
			if err := tagRows.Scan(&tagName); err == nil {
				resp.Tags = append(resp.Tags, tagName)
			}
		}
		tagRows.Close()
	}

	// Generate presigned URL for streaming if ready
	if resp.Status == "ready" {
		client, err := getMinioClient()
		if err == nil {
			s3Key := s3KeyProcessed
			if s3Key == "" {
				s3Key = s3KeyOriginal
			}
			streamURL, err := client.PresignedGetObject(ctx, getS3Bucket(), s3Key, 4*time.Hour, nil)
			if err == nil {
				resp.StreamURL = streamURL.String()
			}
		}
	}

	return &resp, nil
}

// DeleteMediaResponse confirms deletion
type DeleteMediaResponse struct {
	Success bool `json:"success"`
}

// DeleteMedia deletes a media item and its S3 objects
//
//encore:api auth method=DELETE path=/media/:id
func DeleteMedia(ctx context.Context, id string) (*DeleteMediaResponse, error) {
	userData := auth.Data().(*authpkg.UserData)

	// Verify ownership and get S3 keys
	var ownerID int64
	var s3KeyOriginal, s3KeyProcessed string
	err := db.QueryRow(ctx, `
		SELECT owner_id, s3_key_original, COALESCE(s3_key_processed, '')
		FROM media WHERE id = $1
	`, id).Scan(&ownerID, &s3KeyOriginal, &s3KeyProcessed)

	if err != nil {
		return nil, errs.B().Code(errs.NotFound).Msg("media not found").Err()
	}

	if ownerID != userData.UserID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("not authorized").Err()
	}

	// Delete from S3
	client, err := getMinioClient()
	if err == nil {
		_ = client.RemoveObject(ctx, getS3Bucket(), s3KeyOriginal, minio.RemoveObjectOptions{})
		if s3KeyProcessed != "" {
			_ = client.RemoveObject(ctx, getS3Bucket(), s3KeyProcessed, minio.RemoveObjectOptions{})
		}
	}

	// Delete from database (cascade will remove media_tags)
	_, err = db.Exec(ctx, `DELETE FROM media WHERE id = $1`, id)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to delete media").Err()
	}

	return &DeleteMediaResponse{Success: true}, nil
}

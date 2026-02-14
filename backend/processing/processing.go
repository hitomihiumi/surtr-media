// Package processing handles async video transcoding using FFMPEG.
package processing

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"encore.dev/config"
	"encore.dev/pubsub"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"encore.app/media"
)

// Config for S3/MinIO
var cfg struct {
	S3Endpoint  config.String
	S3AccessKey config.String
	S3SecretKey config.String
	S3Bucket    config.String
	S3UseSSL    config.Bool
}

// Database for processing jobs
var db = sqldb.NewDatabase("processing", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// MediaDatabase for updating media status
var mediaDB = sqldb.Named("media")

// getMinioClient creates a MinIO client
func getMinioClient() (*minio.Client, error) {
	return minio.New(cfg.S3Endpoint(), &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3AccessKey(), cfg.S3SecretKey(), ""),
		Secure: cfg.S3UseSSL(),
	})
}

// ProcessMediaSubscription handles media upload events
var _ = pubsub.NewSubscription(media.MediaUploadedTopic, "processing-worker",
	pubsub.SubscriptionConfig[*media.MediaUploaded]{
		Handler: processMedia,
	},
)

func processMedia(ctx context.Context, msg *media.MediaUploaded) error {
	rlog.Info("processing media", "media_id", msg.MediaID, "s3_key", msg.S3Key)

	// Create processing job record
	var jobID string
	err := db.QueryRow(ctx, `
		INSERT INTO processing_jobs (media_id, status, started_at)
		VALUES ($1, 'processing', NOW())
		RETURNING id
	`, msg.MediaID).Scan(&jobID)
	if err != nil {
		rlog.Error("failed to create processing job", "error", err)
	}

	// Update media status to 'processing'
	_, err = mediaDB.Exec(ctx, `UPDATE media SET status = 'processing' WHERE id = $1`, msg.MediaID)
	if err != nil {
		rlog.Error("failed to update media status", "error", err)
		return err
	}

	// Process the video
	processedKey, err := transcodeVideo(ctx, msg.MediaID, msg.S3Key)
	if err != nil {
		rlog.Error("transcoding failed", "error", err, "media_id", msg.MediaID)

		// Update status to failed
		_, _ = mediaDB.Exec(ctx, `UPDATE media SET status = 'failed' WHERE id = $1`, msg.MediaID)
		if jobID != "" {
			_, _ = db.Exec(ctx, `
				UPDATE processing_jobs 
				SET status = 'failed', error_message = $2, completed_at = NOW()
				WHERE id = $1
			`, jobID, err.Error())
		}
		return err
	}

	// Update media with processed key and status
	_, err = mediaDB.Exec(ctx, `
		UPDATE media 
		SET status = 'ready', s3_key_processed = $2 
		WHERE id = $1
	`, msg.MediaID, processedKey)
	if err != nil {
		rlog.Error("failed to update media with processed key", "error", err)
		return err
	}

	// Update processing job as completed
	if jobID != "" {
		_, _ = db.Exec(ctx, `
			UPDATE processing_jobs 
			SET status = 'completed', completed_at = NOW()
			WHERE id = $1
		`, jobID)
	}

	rlog.Info("media processing completed", "media_id", msg.MediaID, "processed_key", processedKey)
	return nil
}

func transcodeVideo(ctx context.Context, mediaID, s3Key string) (string, error) {
	client, err := getMinioClient()
	if err != nil {
		return "", fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Create temp directory for processing
	tempDir, err := os.MkdirTemp("", "media-processing-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Download original file
	inputPath := filepath.Join(tempDir, "input"+filepath.Ext(s3Key))
	object, err := client.GetObject(ctx, cfg.S3Bucket(), s3Key, minio.GetObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get object from S3: %w", err)
	}
	defer object.Close()

	inputFile, err := os.Create(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to create input file: %w", err)
	}

	_, err = io.Copy(inputFile, object)
	inputFile.Close()
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}

	// Prepare output path
	outputPath := filepath.Join(tempDir, "output.mp4")

	// Check if file is a video that needs transcoding
	if !isVideoFile(s3Key) {
		rlog.Info("file is not a video, skipping transcoding", "s3_key", s3Key)
		// For non-video files, just mark as ready without transcoding
		return "", nil
	}

	// Run FFMPEG transcoding
	// Command: ffmpeg -i input -c:v libx265 -crf 28 -preset fast -tag:v hvc1 -c:a aac -movflags +faststart output.mp4
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", inputPath,
		"-c:v", "libx265",
		"-crf", "28",
		"-preset", "fast",
		"-tag:v", "hvc1",
		"-c:a", "aac",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		rlog.Error("ffmpeg failed", "error", err, "output", string(output))
		return "", fmt.Errorf("ffmpeg transcoding failed: %w", err)
	}

	// Get video duration using ffprobe
	duration := getVideoDuration(ctx, outputPath)
	if duration > 0 {
		_, _ = mediaDB.Exec(ctx, `UPDATE media SET duration_seconds = $2 WHERE id = $1`, mediaID, duration)
	}

	// Upload processed file to S3
	processedKey := fmt.Sprintf("processed/%s.mp4", mediaID)

	outputFile, err := os.Open(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to open output file: %w", err)
	}
	defer outputFile.Close()

	stat, err := outputFile.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat output file: %w", err)
	}

	_, err = client.PutObject(ctx, cfg.S3Bucket(), processedKey, outputFile, stat.Size(),
		minio.PutObjectOptions{ContentType: "video/mp4"})
	if err != nil {
		return "", fmt.Errorf("failed to upload processed file: %w", err)
	}

	// Update file size
	_, _ = mediaDB.Exec(ctx, `UPDATE media SET size_bytes = $2 WHERE id = $1`, mediaID, stat.Size())

	return processedKey, nil
}

func isVideoFile(key string) bool {
	ext := strings.ToLower(filepath.Ext(key))
	videoExts := []string{".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpeg", ".mpg", ".3gp"}
	for _, e := range videoExts {
		if ext == e {
			return true
		}
	}
	return false
}

func getVideoDuration(ctx context.Context, filePath string) int {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	)

	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	var duration float64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &duration)
	return int(duration)
}

// JobStatusResponse returns the status of a processing job
type JobStatusResponse struct {
	MediaID      string  `json:"media_id"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message,omitempty"`
}

// GetJobStatus returns the processing status for a media item
//
//encore:api auth method=GET path=/processing/:mediaID/status
func GetJobStatus(ctx context.Context, mediaID string) (*JobStatusResponse, error) {
	var resp JobStatusResponse
	var errorMsg *string

	err := db.QueryRow(ctx, `
		SELECT media_id, status, error_message
		FROM processing_jobs 
		WHERE media_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, mediaID).Scan(&resp.MediaID, &resp.Status, &errorMsg)

	if err != nil {
		// Check media status directly
		err = mediaDB.QueryRow(ctx, `SELECT id, status FROM media WHERE id = $1`, mediaID).Scan(&resp.MediaID, &resp.Status)
		if err != nil {
			return nil, fmt.Errorf("media not found")
		}
		return &resp, nil
	}

	resp.ErrorMessage = errorMsg
	return &resp, nil
}

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

// metadataFields defines the common metadata tags exposed in the UI, in order.
var metadataFields = []string{
	"title",
	"artist",
	"album",
	"album_artist",
	"date",
	"track",
	"genre",
	"composer",
	"comment",
	"publisher",
}

// readMetadata reads metadata tags from an audio file using ffmpeg -f ffmetadata.
// Uses separate stdout/stderr capture so ffmpeg's log output doesn't pollute the result.
func (n *AudioNormalizer) readMetadata(filePath string) (map[string]string, error) {
	cmd := ffmpeg.Command(
		"-i", filePath,
		"-f", "ffmetadata",
		"pipe:1",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	n.logFile.Write("metadata read stderr: " + stderr.String())
	n.logFile.Write("metadata read stdout: " + stdout.String())

	if err != nil && stdout.Len() == 0 {
		return nil, fmt.Errorf("failed to read metadata: %w\n%s", err, stderr.String())
	}

	tags := make(map[string]string)
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, ";") || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			break
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			value := strings.TrimSpace(parts[1])
			tags[key] = value
		}
	}

	n.logFile.Write(fmt.Sprintf("metadata read parsed %d tags: %v", len(tags), tags))
	return tags, nil
}

// writeMetadataTags writes metadata tags to the file using -c copy -metadata key=value,
// the same approach used for ReplayGain tags in processFile.
func (n *AudioNormalizer) writeMetadataTags(filePath string, tags map[string]string) error {
	ext := filepath.Ext(filePath)
	dir := filepath.Dir(filePath)
	tmpFile := filepath.Join(dir, ".tnt_meta_tmp"+ext)

	args := []string{"-i", filePath, "-c", "copy"}

	isM4A := ext == ".m4a" || ext == ".mp4" || ext == ".m4b"
	if isM4A {
		args = append(args, "-movflags", "use_metadata_tags")
	}

	for _, key := range metadataFields {
		value := tags[key]
		args = append(args, "-metadata", key+"="+value)
	}

	args = append(args, "-y", tmpFile)

	n.logFile.Write("metadata write cmd: ffmpeg " + strings.Join(args, " "))

	output, err := ffmpeg.Run(args...)
	n.logFile.Write("metadata write output: " + string(output))

	if err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to write metadata: %w\n%s", err, string(output))
	}

	if err := os.Rename(tmpFile, filePath); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to replace file: %w", err)
	}

	n.logFile.Write("metadata write: replaced original file successfully")
	return nil
}

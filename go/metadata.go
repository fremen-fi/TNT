package main

import (
	"bytes"
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

// metadataEntry is an Entry that triggers a callback when focus is lost.
type metadataEntry struct {
	widget.Entry
	onFocusLost func()
}

func newMetadataEntry(onFocusLost func()) *metadataEntry {
	e := &metadataEntry{onFocusLost: onFocusLost}
	e.ExtendBaseWidget(e)
	return e
}

func (e *metadataEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocusLost != nil {
		e.onFocusLost()
	}
}

// metadataFields defines the common metadata tags to display, in order.
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

	n.logToFile(n.logFile, "metadata read stderr: "+stderr.String())
	n.logToFile(n.logFile, "metadata read stdout: "+stdout.String())

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
		// Skip chapter/stream sections
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

	n.logToFile(n.logFile, fmt.Sprintf("metadata read parsed %d tags: %v", len(tags), tags))
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

	n.logToFile(n.logFile, "metadata write cmd: ffmpeg "+strings.Join(args, " "))

	output, err := ffmpeg.Run(args...)
	n.logToFile(n.logFile, "metadata write output: "+string(output))

	if err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to write metadata: %w\n%s", err, string(output))
	}

	if err := os.Rename(tmpFile, filePath); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to replace file: %w", err)
	}

	n.logToFile(n.logFile, "metadata write: replaced original file successfully")
	return nil
}

// loadMetadataFromSelected reads metadata from the first selected file and populates the UI fields.
func (n *AudioNormalizer) loadMetadataFromSelected() {
	n.mutex.Lock()
	fileCount := len(n.files)
	var filePath string
	if fileCount == 1 {
		filePath = n.files[0]
	}
	n.mutex.Unlock()

	if fileCount == 0 {
		fyne.Do(func() {
			n.metadataStatus.SetText("No file selected")
			n.metadataWriteBtn.Disable()
		})
		return
	}
	if fileCount > 1 {
		fyne.Do(func() {
			n.metadataStatus.SetText("Metadata editing works with single files only")
			n.metadataWriteBtn.Disable()
		})
		return
	}

	fyne.Do(func() {
		n.metadataStatus.SetText("Reading metadata...")
	})

	tags, err := n.readMetadata(filePath)
	if err != nil {
		fyne.Do(func() {
			n.metadataStatus.SetText("Error: " + err.Error())
		})
		n.logStatus("Metadata read error: " + err.Error())
		return
	}

	n.metadataFile = filePath

	fyne.Do(func() {
		for _, field := range metadataFields {
			if entry, ok := n.metadataEntries[field]; ok {
				entry.SetText(tags[field])
			}
		}
		n.metadataStatus.SetText("Loaded: " + filepath.Base(filePath))
		n.metadataWriteBtn.Enable()
	})

	n.logStatus("Loaded metadata from " + filepath.Base(filePath))
}

// writeCurrentMetadata gathers all metadata fields and writes them to the file.
func (n *AudioNormalizer) writeCurrentMetadata() {
	if n.metadataFile == "" {
		return
	}

	// Read entry text directly — the .Text field is safe to read from any goroutine.
	tags := make(map[string]string)
	for _, field := range metadataFields {
		if entry, ok := n.metadataEntries[field]; ok {
			tags[field] = entry.Text
			n.logToFile(n.logFile, fmt.Sprintf("metadata gather: %s = %q", field, entry.Text))
		}
	}

	fyne.Do(func() {
		n.metadataStatus.SetText("Writing metadata...")
		n.metadataWriteBtn.Disable()
	})

	n.logToFile(n.logFile, fmt.Sprintf("metadata write tags: %v", tags))

	err := n.writeMetadataTags(n.metadataFile, tags)

	fyne.Do(func() {
		if err != nil {
			n.metadataStatus.SetText("Error: " + err.Error())
			n.logStatus("Metadata write error: " + err.Error())
		} else {
			n.metadataStatus.SetText("Saved: " + filepath.Base(n.metadataFile))
			n.logStatus("Metadata written to " + filepath.Base(n.metadataFile))
		}
		n.metadataWriteBtn.Enable()
	})
}

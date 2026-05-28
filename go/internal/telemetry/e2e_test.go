package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestEndToEnd verifies that a Client routes events through the anonymizer
// and posts them to the configured endpoint with the expected wire shape.
func TestEndToEnd(t *testing.T) {
	type received struct {
		Events []Event `json:"events"`
	}
	var (
		mu  sync.Mutex
		got []received
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var b received
		if err := json.Unmarshal(body, &b); err != nil {
			t.Errorf("server: bad JSON: %v\n%s", err, body)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		mu.Lock()
		got = append(got, b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New("9.9.9")
	c.SetEndpoint(srv.URL)
	c.SetEnabled(true)
	c.Start()

	c.AppOpen()
	c.FFmpegRun(
		[]string{"-i", "/Users/secret/song.wav", "-metadata", "title=Confidential", "-y", "/tmp/out.flac"},
		[]byte("Input #0, wav, from '/Users/secret/song.wav':\n  Metadata:\n    title    : Confidential\n  Duration: 00:01:00\n"),
		true,
		42*time.Millisecond,
	)

	// Stop drains the queue.
	c.Stop()

	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("no events received")
	}

	var allEvents []Event
	for _, b := range got {
		allEvents = append(allEvents, b.Events...)
	}
	if len(allEvents) != 2 {
		t.Fatalf("want 2 events, got %d", len(allEvents))
	}

	// Verify scrubbing actually happened.
	for _, e := range allEvents {
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "Confidential") {
			t.Errorf("PII leaked: %s", raw)
		}
	}

	// Verify shape of ffmpeg_run.
	var ff *Event
	for i := range allEvents {
		if allEvents[i].Event == "ffmpeg_run" {
			ff = &allEvents[i]
		}
	}
	if ff == nil {
		t.Fatal("missing ffmpeg_run event")
	}
	if ff.ExitOK == nil || !*ff.ExitOK {
		t.Errorf("expected exit_ok=true, got %v", ff.ExitOK)
	}
	if ff.DurationMs != 42 {
		t.Errorf("duration_ms: got %d want 42", ff.DurationMs)
	}
	// title metadata key should remain, value redacted.
	joined := strings.Join(ff.Args, " ")
	if !strings.Contains(joined, "title=<redacted>") {
		t.Errorf("metadata redaction failed: args=%v", ff.Args)
	}
	if ff.AppVersion != "9.9.9" {
		t.Errorf("app_version: got %q want %q", ff.AppVersion, "9.9.9")
	}
	if ff.ClientID == "" {
		t.Error("client_id missing")
	}
}

func TestDisabled_NoPosts(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New("0.0.1")
	c.SetEndpoint(srv.URL)
	// Default: disabled.
	c.Start()
	c.AppOpen()
	c.FFmpegRun([]string{"-version"}, nil, true, 0)
	c.Stop()

	if posts != 0 {
		t.Errorf("expected no posts when disabled, got %d", posts)
	}
}

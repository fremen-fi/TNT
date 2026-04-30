// tnt-telemetry-receiver
//
// Single-endpoint HTTPS receiver for TNT app telemetry. Validates payload
// shape, drops oversized requests, and emits one structured-JSON log line per
// event to stdout — Cloud Logging picks it up automatically and the events
// become queryable via the standard log explorer.
//
// Deliberately minimal: no auth, no DB, no rate limiting beyond Cloud Run's
// concurrency cap. The threat model is "someone discovers the URL and posts
// junk"; mitigations are payload size limits, schema validation, and the fact
// that we drop on the floor whatever we don't recognize.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

const (
	// Cap requests at 256 KiB. A normal batch of 50 events with 4 KiB output
	// tails should land around 200 KiB worst case.
	maxBodyBytes = 256 * 1024

	// Hard cap per individual event field that the receiver enforces in case
	// a misbehaving client doesn't apply its own truncation.
	maxOutputTailBytes = 8 * 1024
	maxArgsCount       = 256
	maxArgLen          = 2048
)

type event struct {
	Event      string    `json:"event"`
	ClientID   string    `json:"client_id"`
	AppVersion string    `json:"app_version"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	TS         time.Time `json:"ts"`

	Args       []string `json:"args,omitempty"`
	ExitOK     *bool    `json:"exit_ok,omitempty"`
	DurationMs int64    `json:"duration_ms,omitempty"`
	OutputTail string   `json:"output_tail,omitempty"`
}

type batch struct {
	Events []event `json:"events"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", handleEvents)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("tnt-telemetry-receiver listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/json" && ct != "application/json; charset=utf-8" {
		http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "body too large or read error", http.StatusRequestEntityTooLarge)
		return
	}

	var b batch
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	accepted := 0
	for _, e := range b.Events {
		if !validate(&e) {
			continue
		}
		emit(e)
		accepted++
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"accepted":%d}`, accepted)
}

func validate(e *event) bool {
	if e.Event != "app_open" && e.Event != "ffmpeg_run" {
		return false
	}
	if e.ClientID == "" || len(e.ClientID) > 64 {
		return false
	}
	if len(e.AppVersion) > 32 || len(e.OS) > 32 || len(e.Arch) > 32 {
		return false
	}
	// Apply server-side truncation as a belt-and-braces measure.
	if len(e.Args) > maxArgsCount {
		e.Args = e.Args[:maxArgsCount]
	}
	for i, a := range e.Args {
		if len(a) > maxArgLen {
			e.Args[i] = a[:maxArgLen]
		}
	}
	if len(e.OutputTail) > maxOutputTailBytes {
		e.OutputTail = e.OutputTail[len(e.OutputTail)-maxOutputTailBytes:]
	}
	return true
}

// emit writes a single structured JSON log line. Cloud Logging promotes the
// `severity` field and indexes the rest as jsonPayload — the result is
// queryable via `jsonPayload.event = "ffmpeg_run"` etc.
func emit(e event) {
	rec := map[string]any{
		"severity":    "INFO",
		"event":       e.Event,
		"client_id":   e.ClientID,
		"app_version": e.AppVersion,
		"os":          e.OS,
		"arch":        e.Arch,
		"ts":          e.TS.Format(time.RFC3339Nano),
	}
	if e.Args != nil {
		rec["args"] = e.Args
	}
	if e.ExitOK != nil {
		rec["exit_ok"] = *e.ExitOK
	}
	if e.DurationMs > 0 {
		rec["duration_ms"] = e.DurationMs
	}
	if e.OutputTail != "" {
		rec["output_tail"] = e.OutputTail
	}
	out, err := json.Marshal(rec)
	if err != nil {
		// Should not happen; fall back to a minimal log line.
		log.Printf("marshal: %v event=%s", err, strconv.Quote(e.Event))
		return
	}
	// fmt.Println goes to stdout, which Cloud Run forwards to Cloud Logging.
	fmt.Println(string(out))
}


package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Default endpoint. Overridable via TNT_TELEMETRY_ENDPOINT env var so we can
// point at a local receiver during dev or self-host without a release.
const defaultEndpoint = "https://tnt-telemetry-uc.a.run.app/v1/events"

// Output captured per ffmpeg invocation. Keep modest — we only need enough
// stderr to diagnose failures and see which encoders/filters real users hit.
const maxOutputTailBytes = 4096

// Event is the over-the-wire shape. The receiver expects this exact schema.
type Event struct {
	Event      string    `json:"event"`
	ClientID   string    `json:"client_id"`
	AppVersion string    `json:"app_version"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	TS         time.Time `json:"ts"`

	// ffmpeg_run only:
	Args       []string `json:"args,omitempty"`
	ExitOK     *bool    `json:"exit_ok,omitempty"`
	DurationMs int64    `json:"duration_ms,omitempty"`
	OutputTail string   `json:"output_tail,omitempty"`

	// app_open / generic events: no extra fields.
}

// Client is a fire-and-forget telemetry sink. Methods are safe to call from
// any goroutine; events are dropped silently if telemetry is disabled or the
// queue is full. The sink never blocks the caller.
type Client struct {
	endpoint   string
	appVersion string
	enabled    atomic.Bool
	queue      chan Event
	http       *http.Client
	stop       chan struct{}
	stopped    chan struct{}
	once       sync.Once
}

// New returns a Client that's wired up but not yet started. Call Start to
// launch the background flusher.
func New(appVersion string) *Client {
	endpoint := defaultEndpoint
	if env := os.Getenv("TNT_TELEMETRY_ENDPOINT"); env != "" {
		endpoint = env
	}
	return &Client{
		endpoint:   endpoint,
		appVersion: appVersion,
		queue:      make(chan Event, 256),
		http:       &http.Client{Timeout: 8 * time.Second},
		stop:       make(chan struct{}),
		stopped:    make(chan struct{}),
	}
}

// SetEnabled toggles telemetry. When disabled, queued events are drained but
// new events become no-ops.
func (c *Client) SetEnabled(b bool) { c.enabled.Store(b) }

// Enabled reports the current toggle state.
func (c *Client) Enabled() bool { return c.enabled.Load() }

// SetEndpoint overrides the default. Used for tests / self-hosting.
func (c *Client) SetEndpoint(url string) {
	if url != "" {
		c.endpoint = url
	}
}

// Start launches the background flusher. Idempotent.
func (c *Client) Start() {
	c.once.Do(func() {
		go c.run()
	})
}

// Stop signals the flusher to exit and waits up to 2s for it to drain. Safe
// to call even if Start was never invoked.
func (c *Client) Stop() {
	select {
	case <-c.stop:
		return
	default:
	}
	close(c.stop)
	select {
	case <-c.stopped:
	case <-time.After(2 * time.Second):
	}
}

// AppOpen records a launch. Should be called once during startup, after the
// user's opt-in preference has been applied.
func (c *Client) AppOpen() {
	c.send(Event{Event: "app_open"})
}

// FFmpegRun records a single ffmpeg invocation. args and output are scrubbed
// here — callers pass the raw values.
func (c *Client) FFmpegRun(args []string, output []byte, exitOK bool, dur time.Duration) {
	scrubbed := ScrubOutput(string(output))
	tail := TailBytes(scrubbed, maxOutputTailBytes)
	ok := exitOK
	c.send(Event{
		Event:      "ffmpeg_run",
		Args:       ScrubArgs(args),
		ExitOK:     &ok,
		DurationMs: dur.Milliseconds(),
		OutputTail: tail,
	})
}

// --- internals ---

func (c *Client) send(e Event) {
	if !c.enabled.Load() {
		return
	}
	e.ClientID = ClientID()
	e.AppVersion = c.appVersion
	e.OS = runtime.GOOS
	e.Arch = runtime.GOARCH
	e.TS = time.Now().UTC()
	select {
	case c.queue <- e:
	default:
		// Queue full; drop. We'd rather lose telemetry than block ffmpeg.
	}
}

func (c *Client) run() {
	defer close(c.stopped)

	const flushInterval = 30 * time.Second
	const maxBatch = 50

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]Event, 0, maxBatch)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		c.post(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-c.stop:
			// Drain anything queued before exiting.
			for {
				select {
				case e := <-c.queue:
					batch = append(batch, e)
					if len(batch) >= maxBatch {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case e := <-c.queue:
			batch = append(batch, e)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

type batchPayload struct {
	Events []Event `json:"events"`
}

func (c *Client) post(events []Event) {
	body, err := json.Marshal(batchPayload{Events: events})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
	// Non-2xx is dropped silently — telemetry is best-effort.
}

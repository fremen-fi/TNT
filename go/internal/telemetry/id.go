package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
)

// ClientID returns a stable anonymous ID for this install. Persisted under
// the user's config dir; generated lazily on first call.
//
// Resetting the ID is a user-visible action: see ResetClientID.
func ClientID() string {
	clientIDOnce.Do(loadOrCreateID)
	return clientID
}

// ResetClientID forces a fresh random ID and persists it. Exposed so the
// Preferences UI can offer a "reset anonymous ID" button.
func ResetClientID() string {
	mu.Lock()
	defer mu.Unlock()
	clientID = newID()
	persist(clientID)
	return clientID
}

var (
	clientIDOnce sync.Once
	mu           sync.Mutex
	clientID     string
)

func idPath() string {
	configDir, _ := os.UserConfigDir()
	return filepath.Join(configDir, "TNT", "telemetry_id")
}

func loadOrCreateID() {
	mu.Lock()
	defer mu.Unlock()

	p := idPath()
	if data, err := os.ReadFile(p); err == nil && len(data) >= 32 {
		clientID = string(data[:32])
		return
	}
	clientID = newID()
	persist(clientID)
}

func persist(id string) {
	p := idPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(id), 0o600)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

package cache

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/models"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewSQLiteStore(dbPath, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSQLiteStore_PutAndGet(t *testing.T) {
	store := newTestStore(t)
	key := BuildKey("GET", "/users", "", "")
	entry := &models.CachedResponse{
		KeyHash:        key,
		Method:         "GET",
		Path:           "/users",
		ResponseStatus: 200,
		ResponseBody:   `[{"id":"1","name":"Alice"}]`,
		Dependency:     "user-service",
	}
	if err := store.Put(entry); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected cached response, got nil")
	}
	if got.ResponseBody != entry.ResponseBody {
		t.Errorf("body mismatch: got %s", got.ResponseBody)
	}
	if got.Dependency != "user-service" {
		t.Errorf("expected user-service, got %s", got.Dependency)
	}
}

func TestSQLiteStore_GetNotFound(t *testing.T) {
	store := newTestStore(t)
	got, err := store.Get("nonexistent-key")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil for missing key")
	}
}

func TestSQLiteStore_GetExpired(t *testing.T) {
	store := newTestStore(t)
	key := BuildKey("GET", "/expired", "", "")
	entry := &models.CachedResponse{
		KeyHash:        key,
		Method:         "GET",
		Path:           "/expired",
		ResponseStatus: 200,
		ResponseBody:   `{"expired":true}`,
		Dependency:     "svc",
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		ExpiresAt:      time.Now().Add(-24 * time.Hour),
	}
	if err := store.Put(entry); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil for expired entry")
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	store := newTestStore(t)
	key := BuildKey("GET", "/delete-me", "", "")
	entry := &models.CachedResponse{
		KeyHash:        key,
		Method:         "GET",
		Path:           "/delete-me",
		ResponseStatus: 200,
		ResponseBody:   "{}",
		Dependency:     "svc",
	}
	if err := store.Put(entry); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(key); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestSQLiteStore_Purge(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 3; i++ {
		key := BuildKey("GET", "/purge", "", string(rune('a'+i)))
		if err := store.Put(&models.CachedResponse{
			KeyHash:        key,
			Method:         "GET",
			Path:           "/purge",
			ResponseStatus: 200,
			ResponseBody:   "{}",
			Dependency:     "svc-a",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Put(&models.CachedResponse{
		KeyHash:        BuildKey("GET", "/other", "", ""),
		Method:         "GET",
		Path:           "/other",
		ResponseStatus: 200,
		ResponseBody:   "{}",
		Dependency:     "svc-b",
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Purge("svc-a")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 deleted, got %d", deleted)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalEntries != 1 {
		t.Errorf("expected 1 remaining, got %d", stats.TotalEntries)
	}
}

func TestSQLiteStore_Stats(t *testing.T) {
	store := newTestStore(t)
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalEntries != 0 {
		t.Errorf("expected 0 entries, got %d", stats.TotalEntries)
	}
	if err := store.Put(&models.CachedResponse{
		KeyHash:        "key1",
		Method:         "GET",
		Path:           "/stats",
		ResponseStatus: 200,
		ResponseBody:   "hello",
		Dependency:     "svc",
	}); err != nil {
		t.Fatal(err)
	}
	stats, err = store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalEntries != 1 {
		t.Errorf("expected 1 entry, got %d", stats.TotalEntries)
	}
	if stats.TotalSize != 5 {
		t.Errorf("expected 5 bytes, got %d", stats.TotalSize)
	}
}

func TestSQLiteStore_PutUpsert(t *testing.T) {
	store := newTestStore(t)
	key := "upsert-key"
	entry := &models.CachedResponse{
		KeyHash:        key,
		Method:         "GET",
		Path:           "/upsert",
		ResponseStatus: 200,
		ResponseBody:   "v1",
		Dependency:     "svc",
	}
	if err := store.Put(entry); err != nil {
		t.Fatal(err)
	}
	entry.ResponseBody = "v2"
	if err := store.Put(entry); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResponseBody != "v2" {
		t.Errorf("expected v2, got %s", got.ResponseBody)
	}
}

func TestBuildKey(t *testing.T) {
	k1 := BuildKey("GET", "/users", "", "")
	k2 := BuildKey("POST", "/users", "", "")
	k3 := BuildKey("GET", "/users", "page=1", "")
	if k1 == k2 {
		t.Error("different methods should produce different keys")
	}
	if k1 == k3 {
		t.Error("different queries should produce different keys")
	}
	if len(k1) != 64 {
		t.Errorf("expected SHA-256 hex (64 chars), got %d", len(k1))
	}
}

func TestIsExpired(t *testing.T) {
	future := &models.CachedResponse{ExpiresAt: time.Now().Add(1 * time.Hour)}
	if IsExpired(future) {
		t.Error("should not be expired")
	}
	past := &models.CachedResponse{ExpiresAt: time.Now().Add(-1 * time.Hour)}
	if !IsExpired(past) {
		t.Error("should be expired")
	}
	zero := &models.CachedResponse{}
	if IsExpired(zero) {
		t.Error("zero time should never expire")
	}
}

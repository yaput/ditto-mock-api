package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// Store is the interface for the response cache.
type Store interface {
	Get(key string) (*models.CachedResponse, error)
	Put(entry *models.CachedResponse) error
	Delete(key string) error
	Purge(dependency string) (int64, error)
	Stats() (*models.CacheStats, error)
	Close() error
}

// BuildKey creates a deterministic cache key from request attributes.
func BuildKey(method, path, query, bodyHash string) string {
	raw := fmt.Sprintf("%s|%s|%s|%s", method, path, query, bodyHash)
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// IsExpired checks if a cached response has expired.
func IsExpired(entry *models.CachedResponse) bool {
	if entry.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(entry.ExpiresAt)
}

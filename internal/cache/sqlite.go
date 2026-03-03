package cache

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// SQLiteStore is a SQLite-backed cache implementation.
type SQLiteStore struct {
	db  *sql.DB
	ttl time.Duration
}

// NewSQLiteStore opens or creates a SQLite database at the given path.
func NewSQLiteStore(dsn string, ttl time.Duration) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating sqlite: %w", err)
	}

	return &SQLiteStore{db: db, ttl: ttl}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS cached_responses (
			key_hash         TEXT PRIMARY KEY,
			method           TEXT NOT NULL,
			path             TEXT NOT NULL,
			query            TEXT NOT NULL DEFAULT '',
			request_body_hash TEXT NOT NULL DEFAULT '',
			response_status  INTEGER NOT NULL,
			response_headers TEXT NOT NULL DEFAULT '{}',
			response_body    TEXT NOT NULL,
			dependency       TEXT NOT NULL,
			created_at       DATETIME NOT NULL,
			expires_at       DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_cached_dependency ON cached_responses(dependency);
		CREATE INDEX IF NOT EXISTS idx_cached_expires ON cached_responses(expires_at);
	`)
	return err
}

// Get retrieves a cached response by key hash. Returns nil if not found or expired.
func (s *SQLiteStore) Get(key string) (*models.CachedResponse, error) {
	row := s.db.QueryRow(`
		SELECT key_hash, method, path, query, request_body_hash,
		       response_status, response_headers, response_body,
		       dependency, created_at, expires_at
		FROM cached_responses
		WHERE key_hash = ?
	`, key)

	var entry models.CachedResponse
	err := row.Scan(
		&entry.KeyHash, &entry.Method, &entry.Path, &entry.Query,
		&entry.RequestBodyHash, &entry.ResponseStatus,
		&entry.ResponseHeaders, &entry.ResponseBody,
		&entry.Dependency, &entry.CreatedAt, &entry.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning cached response: %w", err)
	}

	if IsExpired(&entry) {
		_ = s.Delete(key)
		return nil, nil
	}

	return &entry, nil
}

// Put inserts or replaces a cached response.
func (s *SQLiteStore) Put(entry *models.CachedResponse) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if entry.ExpiresAt.IsZero() && s.ttl > 0 {
		entry.ExpiresAt = entry.CreatedAt.Add(s.ttl)
	}

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO cached_responses
		(key_hash, method, path, query, request_body_hash,
		 response_status, response_headers, response_body,
		 dependency, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.KeyHash, entry.Method, entry.Path, entry.Query,
		entry.RequestBodyHash, entry.ResponseStatus,
		entry.ResponseHeaders, entry.ResponseBody,
		entry.Dependency, entry.CreatedAt, entry.ExpiresAt,
	)
	return err
}

// Delete removes a cached response by key hash.
func (s *SQLiteStore) Delete(key string) error {
	_, err := s.db.Exec(`DELETE FROM cached_responses WHERE key_hash = ?`, key)
	return err
}

// Purge removes all cached responses for a dependency. Returns rows deleted.
func (s *SQLiteStore) Purge(dependency string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM cached_responses WHERE dependency = ?`, dependency)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Stats returns cache statistics.
func (s *SQLiteStore) Stats() (*models.CacheStats, error) {
	var stats models.CacheStats
	err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(LENGTH(response_body)), 0) FROM cached_responses`).
		Scan(&stats.TotalEntries, &stats.TotalSize)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

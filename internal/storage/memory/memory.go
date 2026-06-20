// Package memory is an in-process implementation of the storage interfaces, used
// by unit tests and the storage contract suite. It is not for production use.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-doc/internal/core"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage"
)

// Store implements both storage.MetadataStore and storage.BlobStore in memory.
type Store struct {
	mu       sync.RWMutex
	meta     map[string]storage.DocMeta
	comments map[string][]core.Comment
	sessions map[string]sessionEntry
	tokens   map[string]storage.TokenRecord
	blobs    map[string]string // "slug\x00version" -> html
}

type sessionEntry struct {
	data      storage.Session
	expiresAt int64
}

// New returns an empty in-memory store.
func New() *Store {
	return &Store{
		meta:     map[string]storage.DocMeta{},
		comments: map[string][]core.Comment{},
		sessions: map[string]sessionEntry{},
		tokens:   map[string]storage.TokenRecord{},
		blobs:    map[string]string{},
	}
}

// --- MetadataStore ---

func (s *Store) GetMeta(_ context.Context, slug string) (*storage.DocMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.meta[slug]
	if !ok {
		return nil, nil
	}
	return &m, nil
}

func (s *Store) PutMeta(_ context.Context, slug string, meta storage.DocMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta[slug] = meta
	return nil
}

func (s *Store) DeleteMeta(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.meta, slug)
	return nil
}

func (s *Store) ListMeta(_ context.Context) ([]storage.MetaEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]storage.MetaEntry, 0, len(s.meta))
	for slug, m := range s.meta {
		out = append(out, storage.MetaEntry{Slug: slug, Meta: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (s *Store) GetComments(_ context.Context, slug string) ([]core.Comment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.comments[slug]
	if list == nil {
		return []core.Comment{}, nil
	}
	return list, nil
}

func (s *Store) PutComments(_ context.Context, slug string, list []core.Comment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comments[slug] = list
	return nil
}

func (s *Store) DeleteComments(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.comments, slug)
	return nil
}

func (s *Store) GetSession(_ context.Context, sid string) (*storage.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[sid]
	if !ok {
		return nil, nil
	}
	if e.expiresAt < time.Now().UnixMilli() {
		delete(s.sessions, sid)
		return nil, nil
	}
	d := e.data
	return &d, nil
}

func (s *Store) PutSession(_ context.Context, sid string, data storage.Session, ttlSeconds int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sid] = sessionEntry{data: data, expiresAt: time.Now().UnixMilli() + int64(ttlSeconds)*1000}
	return nil
}

func (s *Store) DeleteSession(_ context.Context, sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sid)
	return nil
}

func (s *Store) GetToken(_ context.Context, token string) (*storage.TokenRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tokens[token]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (s *Store) PutToken(_ context.Context, token string, rec storage.TokenRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tokens[token]; exists {
		return nil // ON CONFLICT DO NOTHING
	}
	s.tokens[token] = rec
	return nil
}

func (s *Store) AnyToken(_ context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens) > 0, nil
}

func (s *Store) Close() error { return nil }

// --- BlobStore ---

func blobKey(slug string, version int) string {
	return slug + "\x00" + itoa(version)
}

func (s *Store) PutDoc(_ context.Context, slug string, version int, html string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[blobKey(slug, version)] = html
	return int64(len(html)), nil
}

func (s *Store) GetDoc(_ context.Context, slug string, version int) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.blobs[blobKey(slug, version)]
	return h, ok, nil
}

func (s *Store) HeadDoc(_ context.Context, slug string, version int) (int64, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.blobs[blobKey(slug, version)]
	if !ok {
		return 0, false, nil
	}
	return int64(len(h)), true, nil
}

func (s *Store) ListVersions(_ context.Context, slug string) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := slug + "\x00"
	var out []int
	for k := range s.blobs {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			if n, ok := atoi(k[len(prefix):]); ok {
				out = append(out, n)
			}
		}
	}
	sort.Ints(out)
	return out, nil
}

func (s *Store) DeleteDoc(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := slug + "\x00"
	for k := range s.blobs {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(s.blobs, k)
		}
	}
	return nil
}

// small int<->string helpers to avoid strconv churn in hot paths
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

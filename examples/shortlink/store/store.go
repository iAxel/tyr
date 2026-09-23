// Package store keeps the links of the service in memory.
package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"
)

// Errors of the store. The store knows nothing about tyr: the service
// translates them into tyr errors with tyr.API.MapError.
var (
	ErrNotFound = errors.New("store: link not found")
	ErrExists   = errors.New("store: code is taken")
)

// Link is a stored link.
type Link struct {
	Code      string
	URL       string
	CreatedAt time.Time
}

// Store keeps links in memory. It is safe for concurrent use. Its methods
// take a context, as those of a database would, though they don't need it.
type Store struct {
	mu    sync.RWMutex
	links map[string]Link // by code
}

// New returns an empty store.
func New() *Store {
	return &Store{links: make(map[string]Link)}
}

// Create adds l, or returns ErrExists if its code is taken.
func (s *Store) Create(ctx context.Context, l Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[l.Code]; ok {
		return fmt.Errorf("%w: %s", ErrExists, l.Code)
	}
	s.links[l.Code] = l
	return nil
}

// Get returns the link with the code, or ErrNotFound.
func (s *Store) Get(ctx context.Context, code string) (Link, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.links[code]
	if !ok {
		return Link{}, fmt.Errorf("%w: %s", ErrNotFound, code)
	}
	return l, nil
}

// Delete deletes the link with the code, or returns ErrNotFound.
func (s *Store) Delete(ctx context.Context, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[code]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, code)
	}
	delete(s.links, code)
	return nil
}

// DeleteFunc deletes the links for which del returns true and returns how
// many it deleted.
func (s *Store) DeleteFunc(ctx context.Context, del func(Link) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.links)
	maps.DeleteFunc(s.links, func(_ string, l Link) bool { return del(l) })
	return n - len(s.links)
}

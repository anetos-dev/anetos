// SPDX-License-Identifier: Apache-2.0

package main

import (
	"slices"
	"sync"
	"time"
)

// Note is a note. The example keeps notes in memory; see examples/database
// for the data layer.
type Note struct {
	ID        int64
	Title     string
	Body      string
	CreatedAt time.Time
}

// Store is an in-memory list of notes, safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	next  int64
	notes []Note
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{next: 1} }

// All returns the notes, newest first.
func (s *Store) All() []Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.notes)
	slices.Reverse(out)
	return out
}

// Add stores a new note.
func (s *Store) Add(title, body string) Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := Note{ID: s.next, Title: title, Body: body, CreatedAt: time.Now().UTC()}
	s.next++
	s.notes = append(s.notes, n)
	return n
}

// Get returns the note with id.
func (s *Store) Get(id int64) (Note, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.notes, func(n Note) bool { return n.ID == id })
	if i < 0 {
		return Note{}, false
	}
	return s.notes[i], true
}

// Update changes a note and reports whether it exists.
func (s *Store) Update(id int64, title, body string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.notes, func(n Note) bool { return n.ID == id })
	if i < 0 {
		return false
	}
	s.notes[i].Title, s.notes[i].Body = title, body
	return true
}

// Delete removes a note.
func (s *Store) Delete(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = slices.DeleteFunc(s.notes, func(n Note) bool { return n.ID == id })
}

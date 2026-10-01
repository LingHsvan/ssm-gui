// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// hub fans server-sent events out to every connected page.
type hub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

// subscribe registers a client whose first message is first.
func (h *hub) subscribe(first string) chan string {
	ch := make(chan string, 16)
	ch <- first
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// publish sends msg to every client; a client whose buffer is full misses it
// and catches up with the next status.
func (h *hub) publish(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func statusEvent(st Status) string {
	b, _ := json.Marshal(st)
	return "data: " + string(b) + "\n\n"
}

// updateStatus applies change to the status and pushes the result to every
// page. Pushing under s.mu keeps the pages' view in the same order as the
// changes.
func (s *Server) updateStatus(change func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.status)
	s.publishLocked()
}

// publishLocked pushes the current status to every page; s.mu must be held.
func (s *Server) publishLocked() {
	s.clients.publish(statusEvent(s.status))
}

// handleEvents streams the status to the page: once on connect, then on
// every change.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	s.mu.Lock()
	ch := s.clients.subscribe(statusEvent(s.status))
	s.mu.Unlock()
	defer s.clients.unsubscribe(ch)

	for {
		select {
		case msg := <-ch:
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, st)
}

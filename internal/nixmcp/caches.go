package nixmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const cacheTTL = 6 * time.Hour

type memo[T any] struct {
	mu     sync.Mutex
	ok     bool
	at     time.Time
	val    T
	ttl    time.Duration
	loader func(context.Context) (T, error)
}

func (m *memo[T]) get(ctx context.Context) (T, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ok && (m.ttl == 0 || time.Since(m.at) < m.ttl) {
		return m.val, nil
	}
	v, err := m.loader(ctx)
	if err != nil {
		if m.ok {
			return m.val, nil
		}
		var zero T
		return zero, err
	}
	m.val, m.ok, m.at = v, true, time.Now()
	return m.val, nil
}

var noogleCache = &memo[[]map[string]any]{ttl: cacheTTL, loader: loadNoogle}

func loadNoogle(ctx context.Context) ([]map[string]any, error) {
	status, body, err := httpGet(ctx, noogleAPI, nil, nil, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Noogle data: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("failed to fetch Noogle data: HTTP %d", status)
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse Noogle data: %w", err)
	}
	return payload.Data, nil
}

type nixdevIndex struct {
	Docnames []string         `json:"docnames"`
	Titles   []string         `json:"titles"`
	Terms    map[string][]int `json:"terms"`
}

var nixdevCache = &memo[*nixdevIndex]{ttl: cacheTTL, loader: loadNixdev}

func loadNixdev(ctx context.Context) (*nixdevIndex, error) {
	status, body, err := httpGet(ctx, nixdevIndexURL, nil, nil, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch nix.dev index: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("failed to fetch nix.dev index: HTTP %d", status)
	}
	content := strings.TrimSpace(string(body))
	const prefix = "Search.setIndex("
	if !strings.HasPrefix(content, prefix) {
		return nil, errors.New("failed to parse nix.dev index: unexpected format")
	}
	jsonStr := strings.TrimSuffix(strings.TrimSpace(content[len(prefix):]), ")")

	var raw struct {
		Docnames []string                   `json:"docnames"`
		Titles   []string                   `json:"titles"`
		Terms    map[string]json.RawMessage `json:"terms"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse nix.dev index: %w", err)
	}
	idx := &nixdevIndex{Docnames: raw.Docnames, Titles: raw.Titles, Terms: map[string][]int{}}
	for term, rm := range raw.Terms {
		var asList []int
		if err := json.Unmarshal(rm, &asList); err == nil {
			idx.Terms[term] = asList
		}
	}
	return idx, nil
}

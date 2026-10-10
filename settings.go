// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"anetos.dev/anetos/config"
)

// trackedSource is the app's configuration source. It remembers the keys
// read, for doctor's check of the .env files, and the settings still read
// under their former names, which it logs once each (D311).
type trackedSource struct {
	config.Source
	mu      sync.Mutex
	read    map[string]bool
	former  map[string]string // every former name → current
	renamed map[string]string // the former names read → current
	log     *slog.Logger
}

func newTrackedSource(src config.Source) *trackedSource {
	return &trackedSource{Source: src, read: map[string]bool{}, former: map[string]string{}, renamed: map[string]string{}}
}

// ReportFormer implements config.RenameReporter.
func (s *trackedSource) ReportFormer(old, current string) {
	s.mu.Lock()
	s.former[old] = current
	s.mu.Unlock()
}

// Lookup implements config.Source.
func (s *trackedSource) Lookup(key string) (string, bool) {
	s.mu.Lock()
	s.read[key] = true
	s.mu.Unlock()
	return s.Source.Lookup(key)
}

// ReportRename implements config.RenameReporter.
func (s *trackedSource) ReportRename(old, current string) {
	s.mu.Lock()
	_, seen := s.renamed[old]
	s.renamed[old] = current
	log := s.log
	s.mu.Unlock()
	if !seen && log != nil {
		log.Warn(old+" is renamed "+current+": rename it in your settings (the old name is read until v0.6)", "setting", current)
	}
}

// setLogger logs the renames reported before the logger existed.
func (s *trackedSource) setLogger(log *slog.Logger) {
	s.mu.Lock()
	s.log = log
	pending := make([][2]string, 0, len(s.renamed))
	for old, cur := range s.renamed {
		pending = append(pending, [2]string{old, cur})
	}
	s.mu.Unlock()
	slices.SortFunc(pending, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	for _, p := range pending {
		log.Warn(p[0]+" is renamed "+p[1]+": rename it in your settings (the old name is read until v0.6)", "setting", p[1])
	}
}

// Names implements config.Lister when the source it wraps does.
func (s *trackedSource) Names() []string {
	if l, ok := s.Source.(config.Lister); ok {
		return l.Names()
	}
	return nil
}

// othersNames are settings other frameworks use for what Anetos reads
// under another key: doctor points at the right one.
var othersNames = map[string]string{
	"DB_CONNECTION":        "DB_DRIVER",
	"DB_DATABASE":          "DB_NAME",
	"DB_USERNAME":          "DB_USER",
	"CACHE_STORE":          "CACHE_DRIVER",
	"QUEUE_CONNECTION":     "QUEUE_DRIVER",
	"MAIL_MAILER":          "MAIL_DRIVER",
	"FILESYSTEM_DISK":      "STORAGE_DRIVER",
	"BROADCAST_CONNECTION": "PUBSUB_DRIVER",
	"SESSION_LIFETIME":     "SESSION_TTL",
	"LOG_CHANNEL":          "LOG_FORMAT",
}

// settingsChecks reports the settings read under their former names, and
// the keys of the .env files that are another framework's name for a
// setting, a former name left beside its new one, or a typo of a
// setting. A key it can't place is the app's own (or a driver's that
// isn't selected): it says nothing about it.
func (s *trackedSource) settingsChecks(context.Context) []Finding {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Finding
	olds := make([]string, 0, len(s.renamed))
	for old := range s.renamed {
		olds = append(olds, old)
	}
	slices.Sort(olds)
	for _, old := range olds {
		out = append(out, Finding{Warning, fmt.Sprintf("%s is renamed %s: rename it in your settings (%s is read until v0.6)", old, s.renamed[old], old)})
	}
	for _, name := range s.namesLocked() {
		if _, ok := s.renamed[name]; ok {
			continue
		}
		if cur, ok := s.former[name]; ok {
			out = append(out, Finding{Warning, fmt.Sprintf("%s in your .env is the former name of %s, which is set: remove %s", name, cur, name)})
			continue
		}
		if s.read[name] {
			continue
		}
		if to, ok := othersNames[name]; ok {
			out = append(out, Finding{Warning, fmt.Sprintf("%s in your .env isn't a setting Anetos reads: use %s", name, to)})
			continue
		}
		if near := s.nearest(name); near != "" {
			out = append(out, Finding{Warning, fmt.Sprintf("%s in your .env isn't a setting Anetos reads: did you mean %s?", name, near)})
		}
	}
	return out
}

func (s *trackedSource) namesLocked() []string {
	if l, ok := s.Source.(config.Lister); ok {
		return l.Names()
	}
	return nil
}

// nearest returns the read key closest to name, if it's close enough to
// be a typo of it.
func (s *trackedSource) nearest(name string) string {
	keys := make([]string, 0, len(s.read))
	for k := range s.read {
		if _, old := s.former[k]; !old {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	best, bestD := "", len(name)/4+1
	for _, k := range keys {
		if d := distance(name, k); d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

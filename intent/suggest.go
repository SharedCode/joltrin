package intent

import (
	"fmt"
	"sort"
	"sync"
)

// Suggestions turns repeated, owner-approved actions into proposed rules.
// It counts only the shape of an approved action (agent, tool, resource,
// environment), never its params. A suggestion is inert: it does nothing
// until the owner accepts it, and accepting it creates an ordinary explicit
// rule with an expiry the owner chose.
type Suggestions struct {
	mu        sync.Mutex
	threshold int
	counts    map[shape]int
	rejected  map[shape]bool
}

type shape struct{ agent, tool, resource, env string }

// NewSuggestions suggests a rule after threshold approved repeats. A threshold
// below 2 is raised to 2: one approval is not a pattern.
func NewSuggestions(threshold int) *Suggestions {
	if threshold < 2 {
		threshold = 2
	}
	return &Suggestions{threshold: threshold, counts: map[shape]int{}, rejected: map[shape]bool{}}
}

// Observe records one action the owner approved.
func (s *Suggestions) Observe(a Action) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[shape{a.Agent, a.Tool, a.Resource, a.Env}]++
}

// Suggestion is a proposed, inactive rule.
type Suggestion struct {
	ID       string `json:"id"`
	Agent    string `json:"agent"`
	Tool     string `json:"tool"`
	Resource string `json:"resource"`
	Env      string `json:"env"`
	Seen     int    `json:"seen"`
}

func (sh shape) id() string {
	return fmt.Sprintf("%s|%s|%s|%s", sh.agent, sh.tool, sh.resource, sh.env)
}

// Pending lists suggestions that reached the threshold and were not rejected.
func (s *Suggestions) Pending() []Suggestion {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Suggestion
	for sh, n := range s.counts {
		if n >= s.threshold && !s.rejected[sh] {
			out = append(out, Suggestion{ID: sh.id(), Agent: sh.agent, Tool: sh.tool, Resource: sh.resource, Env: sh.env, Seen: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Reject hides a suggestion and stops counting that shape.
func (s *Suggestions) Reject(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sh := range s.counts {
		if sh.id() == id {
			s.rejected[sh] = true
		}
	}
}

// Accept turns a pending suggestion into a rule in v. The owner supplies the
// rule id, label, purpose and expiry; the grant carries no requirements, so
// to keep approval in the loop the owner edits it before accepting.
func (s *Suggestions) Accept(v *Vault, id string, r Rule) error {
	var found *Suggestion
	for _, p := range s.Pending() {
		if p.ID == id {
			p := p
			found = &p
		}
	}
	if found == nil {
		return fmt.Errorf("intent: no pending suggestion %q", id)
	}
	if r.ExpiresAt.IsZero() {
		return fmt.Errorf("intent: an accepted suggestion needs an expiry")
	}
	r.Mode, r.Agent, r.Tool, r.Resource, r.Env = Grant, found.Agent, found.Tool, found.Resource, found.Env
	r.Provenance = Provenance{Source: "suggestion", By: r.Owner, At: v.now().UTC()}
	if err := v.Add(r); err != nil {
		return err
	}
	s.Reject(id)
	return nil
}

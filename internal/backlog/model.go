// Package backlog provides a persistent work queue for the coordinator agent.
package backlog

import "time"

// Item represents a task in the persistent work backlog.
type Item struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	Description    string         `json:"description,omitempty"`
	Priority       string         `json:"priority"`  // "high", "normal", "low"
	Status         string         `json:"status"`    // "pending", "in_progress", "completed", "failed", "blocked"
	Source         string         `json:"source"`    // "user", "coordinator", "sub-agent"
	ParentID       string         `json:"parent_id,omitempty"`
	DeliberationID string         `json:"deliberation_id,omitempty"` // links to strategic deliberation
	Context        map[string]any `json:"context,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	ChildCount     int            `json:"child_count,omitempty"` // populated on read
	DoneCount      int            `json:"done_count,omitempty"`  // populated on read
}

// Approach represents a potential solution strategy evaluated during deliberation.
type Approach struct {
	Name           string   `json:"name"`
	Strategy       string   `json:"strategy"`
	Pros           []string `json:"pros"`
	Cons           []string `json:"cons"`
	SkillsRequired []string `json:"skills_required"`
	Effort         string   `json:"effort"` // low | medium | high
}

// DeliberationRecord stores the result of a strategic deliberation.
type DeliberationRecord struct {
	ID          string     `json:"id"`
	Task        string     `json:"task"`
	Constraints []string   `json:"constraints"`
	Approaches  []Approach `json:"approaches"`
	Selected    string     `json:"selected"`
	Rationale   string     `json:"rationale"`
	Risks       []string   `json:"risks"`
	Mitigations []string   `json:"mitigations"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Filter controls which backlog items are returned by List.
type Filter struct {
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
	Source   string `json:"source,omitempty"`
	ParentID string `json:"parent_id,omitempty"` // filter children of a parent
	TopLevel bool   `json:"top_level,omitempty"` // only items with no parent
	Limit    int    `json:"limit,omitempty"`
}

// Update holds optional fields for partial item updates.
type Update struct {
	Title          *string `json:"title,omitempty"`
	Description    *string `json:"description,omitempty"`
	Priority       *string `json:"priority,omitempty"`
	Status         *string `json:"status,omitempty"`
	ParentID       *string `json:"parent_id,omitempty"`
	DeliberationID *string `json:"deliberation_id,omitempty"`
}

package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Replayer plays back recorded model interactions for deterministic testing.
type Replayer struct {
	mu       sync.Mutex
	sessions map[string]*RecordedSession
	current  string
	index    int // Current turn index within the session
}

// NewReplayer creates a replayer from recorded sessions.
func NewReplayer(sessions []*RecordedSession) *Replayer {
	r := &Replayer{
		sessions: make(map[string]*RecordedSession, len(sessions)),
	}
	for _, s := range sessions {
		r.sessions[s.CaseID] = s
	}
	return r
}

// LoadFromFile loads recorded sessions from a JSONL file.
func LoadFromFile(path string) (*Replayer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var sessions []*RecordedSession
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var session RecordedSession
		if err := json.Unmarshal(scanner.Bytes(), &session); err != nil {
			return nil, fmt.Errorf("parse session: %w", err)
		}
		sessions = append(sessions, &session)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return NewReplayer(sessions), nil
}

// StartSession begins replay for a specific eval case.
func (r *Replayer) StartSession(caseID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.sessions[caseID]; !ok {
		return fmt.Errorf("no recorded session for case %q", caseID)
	}

	r.current = caseID
	r.index = 0
	return nil
}

// GenerateContent implements model.Model, returning recorded responses.
func (r *Replayer) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	session, ok := r.sessions[r.current]
	if !ok {
		return nil, fmt.Errorf("no active replay session")
	}

	if r.index >= len(session.Turns) {
		// Return a stop response if we've exhausted recorded turns
		ch := make(chan *model.Response, 1)
		ch <- &model.Response{
			ID:   "replay-exhausted",
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "[Replay exhausted - no more recorded turns]",
				},
			}},
		}
		close(ch)
		return ch, nil
	}

	// Return the recorded response
	turn := session.Turns[r.index]
	r.index++

	ch := make(chan *model.Response, 1)
	ch <- turn.Response
	close(ch)

	return ch, nil
}

// Info implements model.Model.
func (r *Replayer) Info() model.Info {
	return model.Info{Name: "replay"}
}

// HasSession checks if a recorded session exists for a case.
func (r *Replayer) HasSession(caseID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sessions[caseID]
	return ok
}

// ListSessions returns all recorded case IDs.
func (r *Replayer) ListSessions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	return ids
}

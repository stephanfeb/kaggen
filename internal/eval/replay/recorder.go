// Package replay provides recording and replaying of model interactions.
package replay

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Recorder wraps a model to capture all interactions.
type Recorder struct {
	inner    model.Model
	mu       sync.Mutex
	sessions map[string]*RecordedSession
	current  string // Current session ID
}

// RecordedSession captures all turns for one eval case.
type RecordedSession struct {
	CaseID    string    `json:"case_id"`
	Model     string    `json:"model"`
	Timestamp time.Time `json:"timestamp"`
	Turns     []Turn    `json:"turns"`
}

// Turn represents one request/response pair.
type Turn struct {
	Request  *model.Request  `json:"request"`
	Response *model.Response `json:"response"`
}

// NewRecorder wraps a model for recording.
func NewRecorder(inner model.Model) *Recorder {
	return &Recorder{
		inner:    inner,
		sessions: make(map[string]*RecordedSession),
	}
}

// StartSession begins recording for a new eval case.
func (r *Recorder) StartSession(caseID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = caseID
	r.sessions[caseID] = &RecordedSession{
		CaseID:    caseID,
		Model:     r.inner.Info().Name,
		Timestamp: time.Now(),
		Turns:     make([]Turn, 0),
	}
}

// GenerateContent implements model.Model, recording the interaction.
func (r *Recorder) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	// Call the inner model
	respCh, err := r.inner.GenerateContent(ctx, req)
	if err != nil {
		return nil, err
	}

	// Create a channel to pass through responses while recording
	outCh := make(chan *model.Response, 1)

	go func() {
		defer close(outCh)

		var collectedResp *model.Response
		for resp := range respCh {
			// Pass through the response
			outCh <- resp

			// Collect the final response for recording
			if resp.Done {
				collectedResp = resp
			}
		}

		// Record the turn
		if collectedResp != nil {
			r.mu.Lock()
			if session, ok := r.sessions[r.current]; ok {
				session.Turns = append(session.Turns, Turn{
					Request:  req,
					Response: collectedResp,
				})
			}
			r.mu.Unlock()
		}
	}()

	return outCh, nil
}

// Info implements model.Model.
func (r *Recorder) Info() model.Info {
	return r.inner.Info()
}

// GetSession returns the recorded session for a case.
func (r *Recorder) GetSession(caseID string) *RecordedSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[caseID]
}

// GetAllSessions returns all recorded sessions.
func (r *Recorder) GetAllSessions() []*RecordedSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessions := make([]*RecordedSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// SaveToFile saves all recorded sessions to a JSONL file.
func (r *Recorder) SaveToFile(path string) error {
	r.mu.Lock()
	sessions := make([]*RecordedSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.mu.Unlock()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, session := range sessions {
		if err := enc.Encode(session); err != nil {
			return err
		}
	}

	return nil
}

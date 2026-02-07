package p2p

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/yourusername/kaggen/internal/agent"
	"github.com/yourusername/kaggen/internal/pipeline"
)

// TasksProtocol handles the /kaggen/tasks/1.0.0 protocol.
type TasksProtocol struct {
	*APIHandler
	inFlightStore *agent.InFlightStore
	pipelines     func() []pipeline.Pipeline
}

// NewTasksProtocol creates a new tasks protocol handler.
func NewTasksProtocol(store *agent.InFlightStore, pipelines func() []pipeline.Pipeline, logger *slog.Logger) *TasksProtocol {
	h := &TasksProtocol{
		APIHandler:    NewAPIHandler(TasksProtocolID, logger),
		inFlightStore: store,
		pipelines:     pipelines,
	}

	h.RegisterMethod("list", h.list)
	h.RegisterMethod("cancel", h.cancel)
	h.RegisterMethod("pipelines", h.listPipelines)

	return h
}

// StreamHandler returns the stream handler for this protocol.
func (p *TasksProtocol) StreamHandler() network.StreamHandler {
	return p.HandleStream
}

type listTasksParams struct {
	Status string `json:"status,omitempty"`
}

func (p *TasksProtocol) list(params json.RawMessage) (any, error) {
	var args listTasksParams
	if len(params) > 0 {
		json.Unmarshal(params, &args)
	}

	statusFilter := agent.TaskStatus(args.Status)
	tasks := p.inFlightStore.List(statusFilter)

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].StartedAt.After(tasks[j].StartedAt)
	})

	return map[string]any{"tasks": tasks}, nil
}

type cancelParams struct {
	TaskID string `json:"task_id"`
}

func (p *TasksProtocol) cancel(params json.RawMessage) (any, error) {
	var args cancelParams
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if args.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	if p.inFlightStore.Cancel(args.TaskID) {
		return map[string]any{"success": true, "cancelled": true, "task_id": args.TaskID}, nil
	}

	return map[string]any{"success": false, "cancelled": false, "message": "task not found or not running"}, nil
}

func (p *TasksProtocol) listPipelines(params json.RawMessage) (any, error) {
	pipelines := p.pipelines()
	progress := p.inFlightStore.PipelineProgress()

	type agentSessionKey struct{ session, agent string }
	runningBySession := make(map[agentSessionKey]bool)
	runningGlobal := make(map[string]bool)
	for _, t := range p.inFlightStore.List(agent.TaskRunning) {
		runningBySession[agentSessionKey{t.SessionID, t.AgentName}] = true
		runningGlobal[t.AgentName] = true
	}

	type stageStatus struct {
		Agent       string `json:"agent"`
		Description string `json:"description"`
		Stage       int    `json:"stage"`
		Status      string `json:"status"`
	}
	type pipelineStatus struct {
		Name            string        `json:"name"`
		Description     string        `json:"description"`
		TaskDescription string        `json:"task_description,omitempty"`
		SessionID       string        `json:"session_id,omitempty"`
		Stages          []stageStatus `json:"stages"`
	}

	var result []pipelineStatus

	for _, pl := range pipelines {
		sessionsWithProgress := make(map[string]map[int]bool)
		for sid, pmap := range progress {
			if stages, ok := pmap[pl.Name]; ok {
				sessionsWithProgress[sid] = stages
			}
		}

		if len(sessionsWithProgress) == 0 {
			stages := make([]stageStatus, len(pl.Stages))
			for i, s := range pl.Stages {
				status := "pending"
				if runningGlobal[s.Agent] {
					status = "running"
				}
				stages[i] = stageStatus{Agent: s.Agent, Description: s.Description, Stage: i + 1, Status: status}
			}
			result = append(result, pipelineStatus{Name: pl.Name, Description: pl.Description, Stages: stages})
		} else {
			for sid, completedStages := range sessionsWithProgress {
				stages := make([]stageStatus, len(pl.Stages))
				for i, s := range pl.Stages {
					status := "pending"
					if completedStages[i+1] {
						status = "completed"
					} else if runningBySession[agentSessionKey{sid, s.Agent}] {
						status = "running"
					}
					stages[i] = stageStatus{Agent: s.Agent, Description: s.Description, Stage: i + 1, Status: status}
				}
				taskDesc := p.inFlightStore.PipelineDescription(sid, pl.Name)
				result = append(result, pipelineStatus{Name: pl.Name, Description: pl.Description, TaskDescription: taskDesc, SessionID: sid, Stages: stages})
			}
		}
	}

	return map[string]any{"pipelines": result}, nil
}

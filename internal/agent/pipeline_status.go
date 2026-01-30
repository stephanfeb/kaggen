package agent

import (
	"context"

	"github.com/yourusername/kaggen/internal/pipeline"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// pipelineStatusRequest is the input schema for the pipeline_status tool.
type pipelineStatusRequest struct {
	PipelineName string `json:"pipeline_name,omitempty" jsonschema:"description=Optional pipeline name to check. If empty returns all pipelines."`
}

// pipelineStageInfo describes a single stage's current status.
type pipelineStageInfo struct {
	Stage       int    `json:"stage"`
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Status      string `json:"status"` // pending, running, completed
}

// pipelineInfo describes a pipeline's current progress.
type pipelineInfo struct {
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	TaskDescription string              `json:"task_description,omitempty"`
	SessionID       string              `json:"session_id,omitempty"`
	Stages          []pipelineStageInfo `json:"stages"`
}

// pipelineStatusResponse is the output schema for the pipeline_status tool.
type pipelineStatusResponse struct {
	Pipelines []pipelineInfo `json:"pipelines"`
}

type pipelineStatusChecker struct {
	store     *InFlightStore
	pipelines []pipeline.Pipeline
}

func (c *pipelineStatusChecker) check(_ context.Context, req pipelineStatusRequest) (pipelineStatusResponse, error) {
	progress := c.store.PipelineProgress()

	// Determine which agents currently have running tasks, keyed by session+agent
	// so concurrent pipelines in different sessions don't bleed into each other.
	type agentSessionKey struct{ session, agent string }
	runningBySession := make(map[agentSessionKey]bool)
	runningGlobal := make(map[string]bool)
	for _, t := range c.store.List(TaskRunning) {
		runningBySession[agentSessionKey{t.SessionID, t.AgentName}] = true
		runningGlobal[t.AgentName] = true
	}

	var result []pipelineInfo

	for _, p := range c.pipelines {
		if req.PipelineName != "" && p.Name != req.PipelineName {
			continue
		}

		// Collect sessions with progress for this pipeline.
		sessionsWithProgress := make(map[string]map[int]bool)
		for sid, pmap := range progress {
			if stages, ok := pmap[p.Name]; ok {
				sessionsWithProgress[sid] = stages
			}
		}

		buildStages := func(sessionID string, completedStages map[int]bool) []pipelineStageInfo {
			stages := make([]pipelineStageInfo, len(p.Stages))
			for i, s := range p.Stages {
				status := "pending"
				if completedStages != nil && completedStages[i+1] {
					status = "completed"
				} else if sessionID != "" && runningBySession[agentSessionKey{sessionID, s.Agent}] {
					status = "running"
				} else if sessionID == "" && runningGlobal[s.Agent] {
					status = "running"
				}
				stages[i] = pipelineStageInfo{
					Stage:       i + 1,
					Agent:       s.Agent,
					Description: s.Description,
					Status:      status,
				}
			}
			return stages
		}

		if len(sessionsWithProgress) == 0 {
			result = append(result, pipelineInfo{
				Name:        p.Name,
				Description: p.Description,
				Stages:      buildStages("", nil),
			})
		} else {
			for sid, completed := range sessionsWithProgress {
				result = append(result, pipelineInfo{
					Name:            p.Name,
					Description:     p.Description,
					TaskDescription: c.store.PipelineDescription(sid, p.Name),
					SessionID:       sid,
					Stages:          buildStages(sid, completed),
				})
			}
		}
	}

	return pipelineStatusResponse{Pipelines: result}, nil
}

// NewPipelineStatusTool creates a tool that reports on pipeline progress.
func NewPipelineStatusTool(store *InFlightStore, pipelines []pipeline.Pipeline) tool.Tool {
	c := &pipelineStatusChecker{store: store, pipelines: pipelines}
	return function.NewFunctionTool(
		c.check,
		function.WithName("pipeline_status"),
		function.WithDescription("Check the progress of pipelines. Returns each pipeline's stages with their current status (pending, running, completed). Use this to report pipeline progress to the user."),
	)
}

// Package pipeline provides declarative pipeline definitions loaded from YAML.
// Each pipeline defines a sequence of agent stages that the coordinator dispatches
// in order, with optional failure-retry policies.
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Stage represents a single step in a pipeline.
type Stage struct {
	Agent       string `yaml:"agent"`
	Description string `yaml:"description"`
	OnFail      string `yaml:"on_fail,omitempty"`  // agent name to loop back to on failure
	MaxRetries  int    `yaml:"max_retries,omitempty"`
}

// Pipeline is a declarative definition of an agent orchestration sequence.
type Pipeline struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Trigger     string  `yaml:"trigger"`
	Stages      []Stage `yaml:"stages"`
}

// LoadAll loads all pipeline YAML files from the given directory.
// Returns nil (no error) if the directory does not exist.
func LoadAll(dir string) ([]Pipeline, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pipeline dir %s: %w", dir, err)
	}

	var pipelines []Pipeline
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		p, err := loadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", e.Name(), err)
		}
		pipelines = append(pipelines, p)
	}
	return pipelines, nil
}

func loadFile(path string) (Pipeline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Pipeline{}, err
	}
	var p Pipeline
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Pipeline{}, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if p.Name == "" {
		return Pipeline{}, fmt.Errorf("%s: pipeline name is required", filepath.Base(path))
	}
	if len(p.Stages) == 0 {
		return Pipeline{}, fmt.Errorf("%s: pipeline must have at least one stage", filepath.Base(path))
	}
	return p, nil
}

// BuildInstruction generates coordinator instruction text for a set of pipelines.
// This replaces hardcoded pipeline sections in agent.go.
func BuildInstruction(pipelines []Pipeline) string {
	if len(pipelines) == 0 {
		return ""
	}

	var b strings.Builder
	for _, p := range pipelines {
		b.WriteString(fmt.Sprintf("\n## %s Pipeline\n\n", titleCase(p.Name)))
		b.WriteString(fmt.Sprintf("Trigger: %s\n\n", p.Trigger))
		b.WriteString(fmt.Sprintf("IMPORTANT: For requests matching this trigger, follow this pipeline. NEVER skip stages or dispatch agents out of order.\n\n"))

		for i, s := range p.Stages {
			b.WriteString(fmt.Sprintf("%d. Dispatch `%s` — %s\n", i+1, s.Agent, s.Description))
		}

		// Document retry policies.
		for _, s := range p.Stages {
			if s.OnFail != "" {
				retries := s.MaxRetries
				if retries == 0 {
					retries = 3
				}
				b.WriteString(fmt.Sprintf("\nIf `%s` reports failure: re-dispatch `%s`, then `%s` again (max %d loops).\n",
					s.Agent, s.OnFail, s.Agent, retries))
			}
		}

		b.WriteString("\nAlways use async dispatch (dispatch_task) with policy=auto for each stage.\n")
		b.WriteString("Wait for each stage to complete before dispatching the next — the pipeline is sequential.\n")
	}

	return b.String()
}

// PipelineAgent records which pipeline and stage index an agent belongs to.
type PipelineAgent struct {
	Pipeline string
	Stage    int // 1-based
}

// AgentSet returns a map of agent name → PipelineAgent for all agents that
// appear in any pipeline stage. Used to annotate the coordinator's sub-agent
// list so it knows which agents are pipeline-gated.
func AgentSet(pipelines []Pipeline) map[string]PipelineAgent {
	m := make(map[string]PipelineAgent)
	for _, p := range pipelines {
		for i, s := range p.Stages {
			if _, exists := m[s.Agent]; !exists {
				m[s.Agent] = PipelineAgent{Pipeline: p.Name, Stage: i + 1}
			}
		}
	}
	return m
}

// FindAgentAtStage returns the agent name at the given 1-based stage index
// in the named pipeline. Returns "" if not found.
func FindAgentAtStage(pipelines []Pipeline, pipelineName string, stage int) string {
	for _, p := range pipelines {
		if p.Name == pipelineName && stage >= 1 && stage <= len(p.Stages) {
			return p.Stages[stage-1].Agent
		}
	}
	return ""
}

func titleCase(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

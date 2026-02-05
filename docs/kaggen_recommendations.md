# Kaggen: Closing the Gap

Concrete recommendations for transforming Kaggen from a skill orchestration system to a creative reasoning system with autonomous skill acquisition.

---

## Executive Summary

Kaggen has a solid foundation: the coordinator/specialist pattern, async dispatch, human approval, and hot-reload are production-quality. However, six gaps prevent it from achieving its stated vision as an open-ended reasoning system that can acquire its own capabilities.

This document provides concrete, implementable recommendations for each gap, prioritized by impact and effort, with specific file paths and integration points.

---

## Current State Assessment

| Capability | Status | Notes |
|------------|--------|-------|
| Personal assistant | Strong | Coordinator handles open-ended requests |
| Skill-based delegation | Excellent | Hot-reload, tool filtering, guarded tools |
| Clarification handling | Good | Embedded logic in buildInstruction |
| Epistemic memory | Excellent | Four-way recall, entity graph, background synthesis |
| Forward-planning | Partial | backlog_decompose exists but is operational, not strategic |
| Creative problem-solving | Weak | No exploration or synthesis mechanisms |
| Autonomous skill acquisition | Missing | Human-only skill creation |
| Deep reasoning | Missing | Coordinator optimized for routing, not reasoning |
| Execution context | Missing | No session-scoped state tracking for decision support |

---

## Recommendations

### 1. Autonomous Skill Acquisition (P1 - High Impact)

**Problem**: The agent cannot recognize capability gaps and proactively create new skills. The `skill-builder` skill exists and handles scaffolding/validation/installation, but:
- The coordinator doesn't recognize when it lacks a needed capability
- There's no research phase to determine what a new skill should contain
- Skill creation only happens when explicitly requested by the user

**Solution**: Enable the coordinator to autonomously detect capability gaps, research requirements, and dispatch to `skill-builder` — without waiting for the user to explicitly request skill creation.

#### Components

**1. Gap Detection Logic**

Add coordinator instructions for recognizing capability gaps:

```
## Skill Gap Detection

When processing a task, if you determine that:
- No existing skill matches the required capability
- The task cannot be accomplished by creative use of existing skills
- A new tool/integration would be needed

Then you have identified a SKILL GAP. Do not simply fail or ask the user to create a skill manually.
```

**2. Research-First Workflow**

Before invoking skill-builder, use the researcher agent (see Recommendation 5) to gather requirements:

```
## Skill Acquisition Workflow

When you identify a skill gap:

1. **Research** - Dispatch the `researcher` agent to:
   - Find documentation for the required tool/API
   - Identify installation steps and dependencies
   - Discover usage patterns and examples
   - Determine if CLI tools exist or if custom code is needed

2. **Specify** - Based on research, determine:
   - Skill type: LLM agent (wrapping CLI) vs delegate (complex reasoning)
   - Required tools: which tools the skill needs access to
   - Scripts needed: for LLM skills, what commands to wrap
   - Model: haiku for simple, sonnet for balanced, opus for complex

3. **Build** - Dispatch `skill-builder` with a detailed specification:
   - Clear description of what the skill does
   - The research findings for context
   - Explicit agent type and tool requirements

4. **Reload** - After skill-builder completes, remind user to reload:
   `kill -HUP $(pgrep kaggen)`

5. **Retry** - Re-attempt the original task with the new skill
```

**3. Automatic Reload Tool (Optional Enhancement)**

Add a simple tool to trigger reload without user intervention:

```go
// internal/tools/reload.go

type reloadSkillsArgs struct{}

type reloadSkillsResult struct {
    Success bool   `json:"success"`
    Message string `json:"message"`
}

func (t *ReloadSkillsTool) Execute(ctx context.Context, args reloadSkillsArgs) (*reloadSkillsResult, error) {
    err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
    if err != nil {
        return &reloadSkillsResult{Success: false, Message: err.Error()}, nil
    }
    return &reloadSkillsResult{Success: true, Message: "Skills reloaded"}, nil
}
```

#### Coordinator Instruction Updates

Add to `buildInstruction()` in `internal/agent/agent.go`:

```
## Autonomous Skill Acquisition

You have the ability to extend your own capabilities. When you encounter a task that no existing skill can handle:

1. Do NOT simply tell the user "I don't have a skill for that"
2. Instead, recognize this as an opportunity to acquire a new capability

**Workflow:**
1. Dispatch `researcher` to investigate: "Research how to [capability]. Find installation, usage, and examples."
2. Analyze findings to determine skill type (LLM wrapper vs delegate)
3. Dispatch `skill-builder` with: "Create a skill named [name] that [description]. Type: [llm/delegate]. Based on this research: [findings]"
4. Use `reload_skills` to hot-reload (or remind user)
5. Retry the original task

**When to acquire skills:**
- Task requires a tool/API you don't have access to
- Task requires domain knowledge not in existing skills
- A pattern is likely to recur (worth the investment)

**When NOT to:**
- Existing skills can handle it with creativity
- One-off task unlikely to recur
- User explicitly wants manual control
```

#### Integration with Existing skill-builder

The `skill-builder` skill already provides:
- `scaffold.sh` - Create skill skeleton
- `validate.sh` - Lint and verify
- `install.sh` - Install to ~/.kaggen/skills/

The coordinator's job is to:
1. Detect the gap (new)
2. Research requirements (new, via researcher)
3. Provide skill-builder with good specifications (enhanced instructions)
4. Trigger reload (new tool or reminder)

#### Files to Modify
- `internal/agent/agent.go` (update buildInstruction with acquisition workflow)
- `internal/tools/reload.go` (new, optional - simple SIGHUP trigger)
- `defaults/skills/researcher/SKILL.md` (new, see Recommendation 5)

---

### 2. Tiered Reasoning Architecture (P1 - High Impact)

**Problem**: The coordinator uses a fast/cheap routing model. Deep reasoning is delegated to sub-agents who lack the full context.

**Solution**: Add a reasoning escalation mechanism that invokes a deeper model for complex tasks while preserving fast routing for simple ones.

#### Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    User Request                          │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────┐
│  Tier 1: Routing Model (Sonnet/Haiku)                   │
│  - Fast skill matching                                   │
│  - Simple task dispatch                                  │
│  - Clarification handling                                │
│  - Complexity assessment                                 │
└─────────────────────────────────────────────────────────┘
                           │
              ┌────────────┴────────────┐
              │ Escalation Triggers:     │
              │ - High complexity score  │
              │ - "design/architect" in  │
              │   task                   │
              │ - No clear skill match   │
              │ - User requests thorough │
              │ - >5 subtasks needed     │
              └────────────┬────────────┘
                           ▼
┌─────────────────────────────────────────────────────────┐
│  Tier 2: Reasoning Model (Opus)                         │
│  - Deep multi-step reasoning                             │
│  - Novel problem decomposition                           │
│  - Trade-off analysis                                    │
│  - Analogy and synthesis                                 │
└─────────────────────────────────────────────────────────┘
```

#### Tool Schema

```go
// internal/tools/reasoning.go

type reasoningEscalateArgs struct {
    Task       string            `json:"task"`        // The complex task
    Reason     string            `json:"reason"`      // Why escalation needed
    Context    string            `json:"context"`     // Relevant background
    WorldState map[string]string `json:"world_state"` // Current project state
}

type reasoningEscalateResult struct {
    Analysis     string   `json:"analysis"`      // Deep analysis of the problem
    Approaches   []Approach `json:"approaches"`  // Evaluated options
    SelectedPlan string   `json:"selected_plan"` // Recommended approach
    Confidence   float64  `json:"confidence"`    // 0-1 confidence score
    NextSteps    []string `json:"next_steps"`    // Concrete actions
}

type Approach struct {
    Name           string   `json:"name"`
    Strategy       string   `json:"strategy"`
    Pros           []string `json:"pros"`
    Cons           []string `json:"cons"`
    SkillsRequired []string `json:"skills_required"`
    Effort         string   `json:"effort"` // low | medium | high
}
```

#### World Model (Execution Context)

Add a lightweight world model to track session execution state. This is **distinct from Epistemic Memory** and must remain a separate system.

##### World Model vs. Epistemic Memory

Kaggen already has a sophisticated Epistemic Memory system (see `docs/EPISTEMIC_MEMORY.md`) that provides:
- Long-term storage of facts, experiences, opinions, and observations
- Four-way recall (vector, keyword, entity graph, temporal)
- Background synthesis creating observations from patterns
- Confidence evolution for opinions

The World Model serves a fundamentally different purpose:

| Dimension | Epistemic Memory | World Model |
|-----------|-----------------|-------------|
| **Lifecycle** | Persistent across sessions | Ephemeral, session-scoped |
| **Access pattern** | Search when relevant | Consult before every decision |
| **Update frequency** | After conversations, hourly synthesis | After every tool call |
| **Data model** | Unstructured text + metadata | Structured state machine |
| **Core question** | "What do I know about this user?" | "What's happening right now?" |

##### Why They Must Stay Separate

**1. Different lifecycles require different storage**

Epistemic Memory should survive indefinitely—a user preference learned in January is still relevant in December. World Model state should reset each session—knowing that `main.go` was modified in a previous session is noise, not signal.

**2. Different access patterns require different architectures**

Epistemic Memory is searched on-demand when context might be relevant. World Model must be consulted constantly—before every routing decision, after every tool call. Merging them would either slow down execution (searching when we should just look up) or pollute memory with ephemeral state.

**3. Decision support requires temporal reasoning**

The World Model's key capability is enabling execution-aware decisions:

- "I've called `go build` 3 times and it keeps failing" → try a different approach
- "Tests were passing before I edited `auth.go`" → my change broke something
- "This task started 10 minutes ago with no progress" → might be stuck
- "The user just rejected an approval" → adjust strategy

These require tracking **state transitions within a session**, not recalling knowledge. Epistemic Memory is designed for recall, not for monitoring execution flow.

##### World Model Implementation

```go
// internal/agent/worldmodel.go

type WorldModel struct {
    mu            sync.RWMutex
    sessionID     string
    startedAt     time.Time

    // Execution state
    filesModified map[string]time.Time    // path -> last modified
    toolCalls     []ToolCallRecord        // chronological log
    taskStatus    map[string]TaskState    // task_id -> state with timestamps

    // Decision support state
    errorStreak   int                     // consecutive errors (same tool)
    lastError     *ErrorRecord            // most recent error details
    stateChanges  []StateTransition       // for temporal reasoning

    // Test/build status
    testStatus    TestStatus              // passing | failing | unknown
    lastTestRun   time.Time
}

type ToolCallRecord struct {
    Timestamp  time.Time
    Tool       string
    Args       map[string]any
    Success    bool
    Duration   time.Duration
    ErrorMsg   string  // if failed
}

type StateTransition struct {
    Timestamp time.Time
    From      string
    To        string
    Trigger   string  // what caused the transition
}
```

##### Decision Support Methods

```go
// Should we try a different approach?
func (w *WorldModel) ShouldPivot() (bool, string) {
    if w.errorStreak >= 3 {
        return true, fmt.Sprintf("failed %d consecutive times with same approach", w.errorStreak)
    }
    return false, ""
}

// Should we escalate to deeper reasoning?
func (w *WorldModel) ShouldEscalate() (bool, string) {
    // Check if we've been stuck
    if time.Since(w.startedAt) > 10*time.Minute && len(w.stateChanges) < 2 {
        return true, "minimal progress after 10 minutes"
    }
    return false, ""
}

// Is the current approach working?
func (w *WorldModel) IsProgressingwell() bool {
    recent := w.recentToolCalls(5)
    successes := 0
    for _, tc := range recent {
        if tc.Success {
            successes++
        }
    }
    return successes >= 3  // at least 3 of last 5 succeeded
}

// Get context for reasoning escalation
func (w *WorldModel) GetExecutionSummary() string {
    // Summarize: files touched, errors encountered, time elapsed, current status
}
```

##### Integration with Epistemic Memory

The World Model can **query** Epistemic Memory for context without merging:

```go
// Enrich world model with relevant long-term knowledge
func (w *WorldModel) EnrichFromMemory(memSvc *memory.Service, ctx context.Context) error {
    // Get user preferences relevant to current execution
    prefs, err := memSvc.SearchMemories(ctx, "preferences programming tools", 5)
    if err != nil {
        return err
    }
    w.userPreferences = extractPreferences(prefs)
    return nil
}
```

Epistemic Memory can **observe** World Model patterns over many sessions:

```go
// After session ends, memory extractor might note:
// "User tends to retry failed builds immediately rather than investigating"
// This becomes an observation in Epistemic Memory
```

##### Storage Location

World Model lives in `InFlightStore` (session-scoped), **not** in the SQLite memory database:

```go
// internal/agent/async.go - extend InFlightStore

type InFlightStore struct {
    // ... existing fields ...
    worldModels map[string]*WorldModel  // sessionID -> world model
}

func (s *InFlightStore) GetWorldModel(sessionID string) *WorldModel {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.worldModels[sessionID]
}

func (s *InFlightStore) UpdateWorldModel(sessionID string, update func(*WorldModel)) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if wm, ok := s.worldModels[sessionID]; ok {
        update(wm)
    }
}
```

#### Escalation Heuristics

Implement in coordinator instruction or as pre-dispatch logic:

```go
func shouldEscalate(task string, worldState *WorldState) (bool, string) {
    // Keyword triggers
    keywords := []string{"design", "architect", "evaluate", "analyze", "compare"}
    for _, kw := range keywords {
        if strings.Contains(strings.ToLower(task), kw) {
            return true, "task contains architectural keyword"
        }
    }

    // Complexity heuristics
    if estimatedSubtasks(task) > 5 {
        return true, "task requires many subtasks"
    }

    // No clear skill match (from previous routing attempt)
    if worldState.LastRoutingConfidence < 0.5 {
        return true, "low routing confidence"
    }

    return false, ""
}
```

#### Configuration

```yaml
# ~/.kaggen/config.yaml
reasoning:
  tier2_model: opus  # Model for deep reasoning
  escalation_threshold: 0.5  # Confidence below this triggers escalation
  auto_escalate_keywords:
    - design
    - architect
    - evaluate options
    - trade-off
```

#### Files to Modify
- `internal/agent/worldmodel.go` (new - execution context tracking)
- `internal/tools/reasoning.go` (new - escalation tool)
- `internal/agent/async.go` (integrate WorldModel into InFlightStore)
- `internal/agent/agent.go` (add reasoning_escalate tool, update instructions)
- `internal/config/config.go` (add reasoning config)

---

### 3. Strategic Deliberation Tool (P2 - Medium Impact)

**Problem**: `backlog_decompose` breaks tasks into subtasks but doesn't evaluate alternatives. Planning is operational (how to execute) not strategic (what approach to take).

**Solution**: Add a `plan_deliberate` tool that forces explicit reasoning about alternatives before committing to a plan.

#### Tool Schema

```go
// internal/tools/deliberation.go

type planDeliberateArgs struct {
    Task              string   `json:"task"`               // The task to deliberate on
    Constraints       []string `json:"constraints"`        // e.g., ["time", "quality", "cost"]
    ExplorationBudget int      `json:"exploration_budget"` // Number of approaches to consider (default 3)
    MustConsider      []string `json:"must_consider"`      // Approaches that must be evaluated
}

type planDeliberateResult struct {
    DeliberationID string     `json:"deliberation_id"` // For linking to backlog
    Approaches     []Approach `json:"approaches"`      // Evaluated options
    Selected       string     `json:"selected"`        // Chosen approach name
    Rationale      string     `json:"rationale"`       // Why this approach
    Risks          []string   `json:"risks"`           // Identified risks
    Mitigations    []string   `json:"mitigations"`     // How to handle risks
}
```

#### Integration with Backlog

Extend `backlog_decompose` to optionally link to a deliberation:

```go
// internal/tools/backlog.go - extend backlogDecomposeArgs

type backlogDecomposeArgs struct {
    // ... existing fields ...
    DeliberationID string `json:"deliberation_id,omitempty"` // Link to prior deliberation
}
```

Store deliberation results:

```go
// internal/backlog/model.go - extend Item

type Item struct {
    // ... existing fields ...
    DeliberationID string `json:"deliberation_id,omitempty"`
}
```

#### Coordinator Instructions

```
## Strategic Planning

For complex tasks (3+ steps, multiple approaches possible, significant impact):

1. Use `plan_deliberate` to explore approaches before committing
2. Review the generated approaches and rationale
3. Use `backlog_decompose` with the deliberation_id to create the execution plan
4. This creates an audit trail: deliberation → plan → subtasks

Skip deliberation for straightforward tasks with obvious solutions.
```

#### Files to Modify
- `internal/tools/deliberation.go` (new)
- `internal/tools/backlog.go` (add deliberation_id)
- `internal/backlog/model.go` (add DeliberationID field)
- `internal/backlog/store.go` (store/retrieve deliberations)
- `internal/agent/agent.go` (add tool, update instructions)

---

### 4. Exploration and Creativity Tools (P2 - Medium Impact)

**Problem**: No mechanism for trying multiple approaches, finding analogies, or synthesizing partial solutions.

**Solution**: Add exploration budget to async dispatch, plus analogy search and solution synthesis tools.

#### 4A: Exploration Budget

Extend `dispatch_task` to support exploration:

```go
// internal/agent/async.go - extend asyncDispatchRequest

type asyncDispatchRequest struct {
    // ... existing fields ...
    ExplorationBudget  int  `json:"exploration_budget,omitempty"`  // 0 = deterministic
    RetainAlternatives bool `json:"retain_alternatives,omitempty"` // Keep runner-up solutions
}
```

When `exploration_budget > 0`:
1. Dispatch task N times with different prompts (temperature variation or explicit "try approach X")
2. Collect all results
3. Return best result plus alternatives if `retain_alternatives=true`

#### 4B: Analogy Search Tool

```go
// internal/tools/analogy.go

type analogySearchArgs struct {
    ProblemDescription string `json:"problem_description"`
    Domain             string `json:"domain,omitempty"` // e.g., "web", "data", "devops"
    MaxResults         int    `json:"max_results,omitempty"` // default 5
}

type analogySearchResult struct {
    Analogies []Analogy `json:"analogies"`
}

type Analogy struct {
    PastTask     string  `json:"past_task"`    // Description of similar past task
    Solution     string  `json:"solution"`     // How it was solved
    Similarity   float64 `json:"similarity"`   // 0-1 similarity score
    SessionID    string  `json:"session_id"`   // When this was solved
    Adaptation   string  `json:"adaptation"`   // How to adapt to current problem
}
```

Implementation leverages existing memory service:

```go
// internal/memory/service.go - add method

func (s *Service) SearchAnalogies(ctx context.Context, problem string, domain string, limit int) ([]Analogy, error) {
    // 1. Vector search for similar task descriptions
    // 2. Filter by domain if specified
    // 3. Retrieve associated solutions from task completion records
    // 4. Generate adaptation suggestions via LLM
}
```

#### 4C: Solution Synthesis Tool

```go
// internal/tools/synthesis.go

type synthesizeSolutionsArgs struct {
    Goal       string     `json:"goal"`       // What we're trying to achieve
    Approaches []Approach `json:"approaches"` // Partial solutions to combine
}

type Approach struct {
    Name     string `json:"name"`
    Result   string `json:"result"`   // What this approach produced
    Strength string `json:"strength"` // What it does well
    Weakness string `json:"weakness"` // Where it falls short
}

type synthesizeSolutionsResult struct {
    SynthesizedSolution string   `json:"synthesized_solution"`
    IncorporatedFrom    []string `json:"incorporated_from"` // Which approaches contributed
    Rationale           string   `json:"rationale"`
}
```

#### Files to Modify
- `internal/agent/async.go` (add exploration_budget)
- `internal/tools/analogy.go` (new)
- `internal/tools/synthesis.go` (new)
- `internal/memory/service.go` (add SearchAnalogies)
- `internal/agent/agent.go` (add tools, update instructions)

---

### 5. First-Class Research Agent (P2 - Medium Impact)

**Problem**: Browser capability exists but is peripheral. No sophisticated research workflows for documentation lookup, tool discovery, or multi-source synthesis.

**Solution**: Create a dedicated researcher skill and add web search tooling.

#### Web Search Tool

```go
// internal/tools/web_search.go

type webSearchArgs struct {
    Query      string `json:"query"`
    NumResults int    `json:"num_results,omitempty"` // default 5
    Domain     string `json:"domain,omitempty"`      // limit to domain
}

type webSearchResult struct {
    Results []SearchResult `json:"results"`
}

type SearchResult struct {
    Title   string `json:"title"`
    URL     string `json:"url"`
    Snippet string `json:"snippet"`
}
```

Backend options (configure via `~/.kaggen/config.yaml`):
- SearXNG (self-hosted, privacy-focused)
- Brave Search API
- Google Custom Search API

#### Researcher Skill

```yaml
# defaults/skills/researcher/SKILL.md
---
name: researcher
description: Conducts multi-source web research with synthesis and citations
delegate: claude
claude_model: sonnet
claude_tools: Bash,Read,Write,Glob,Grep
---

You are a Research Agent. Your job is to gather, synthesize, and cite information from multiple sources.

## Capabilities

- **Web Search**: Use `web_search` tool for discovery queries
- **URL Fetch**: Use `browser_navigate` and `browser_content` for specific pages
- **Documentation**: Extract and summarize technical documentation
- **Comparison**: Compare multiple sources for accuracy

## Workflow

1. **Clarify** the research question - what specifically is needed?
2. **Search** using multiple strategies:
   - Direct URL for known authoritative sources
   - Web search for discovery
   - GitHub for code examples
   - Official docs for specifications
3. **Extract** relevant information
4. **Verify** across multiple sources when possible
5. **Synthesize** into actionable summary
6. **Cite** all sources with URLs

## Output Format

Always structure research results as:

### Executive Summary
[3-5 sentence overview]

### Key Findings
- Finding 1
- Finding 2
- ...

### Details
[Expanded information organized by topic]

### Sources
1. [Title](URL) - accessed [date]
2. ...

### Confidence
[high | medium | low] - [reasoning]
```

#### Integration with Skill Acquisition

The researcher agent is a key component of autonomous skill acquisition (Recommendation 1). When the coordinator detects a capability gap:

1. **Coordinator dispatches researcher**: "Research how to [capability]. Find installation, usage, and examples."
2. **Researcher returns findings**: Documentation, CLI tools, APIs, installation steps
3. **Coordinator dispatches skill-builder**: Provides findings as context for skill creation

This two-step workflow ensures new skills are based on actual documentation rather than hallucinated capabilities.

#### Files to Modify
- `internal/tools/web_search.go` (new)
- `defaults/skills/researcher/SKILL.md` (new)
- `internal/tools/skill_synthesis.go` (integrate with researcher)
- `internal/config/config.go` (add search API config)

---

### 6. Dynamic Tool Composition (P3 - Lower Priority)

**Problem**: Per-skill tool filtering is safe but limiting. Novel combinations require new skill creation.

**Solution**: Allow skills to request temporary access to additional tools with audit trail.

#### Composable Tools Frontmatter

```yaml
# Example: defaults/skills/coder/SKILL.md
---
name: coder
tools: [Bash, Read, Edit, Write, Glob, Grep]
composable_tools: [browser, web_search, memory_search]  # Can request at runtime
---
```

#### Tool Request Mechanism

```go
// internal/tools/tool_request.go

type toolRequestArgs struct {
    ToolName   string `json:"tool_name"`
    Reason     string `json:"reason"`
    OneTimeUse bool   `json:"one_time_use"` // true = single use, false = rest of task
}

type toolRequestResult struct {
    Granted bool   `json:"granted"`
    Reason  string `json:"reason,omitempty"` // If denied
}
```

#### Implementation

1. **Parse `composable_tools`** in `internal/agent/skills.go`
2. **Add `tool_request` tool** to sub-agents that have composable_tools defined
3. **Audit trail**: Log all tool elevations to task events
4. **Guarded composition**: If requested tool is in guarded_tools, require approval

#### Files to Modify
- `internal/agent/skills.go` (parse composable_tools)
- `internal/tools/tool_request.go` (new)
- `internal/agent/guarded_graph.go` (handle dynamic tool injection)

---

## Implementation Roadmap

### Phase 1: Foundation (Weeks 1-3)
| Task | Priority | Effort |
|------|----------|--------|
| Implement `web_search` tool | P2 | Low |
| Create researcher skill | P2 | Low |
| Add `reload_skills` tool | P1 | Low |
| Update coordinator instructions for skill acquisition | P1 | Medium |

**Milestone**: Agent can research gaps and dispatch to skill-builder autonomously.

### Phase 2: Reasoning (Weeks 4-6)
| Task | Priority | Effort |
|------|----------|--------|
| Add WorldState to InFlightStore | P1 | Medium |
| Implement `reasoning_escalate` tool | P1 | High |
| Add escalation heuristics | P1 | Medium |
| Implement `plan_deliberate` tool | P2 | Medium |

**Milestone**: Agent performs deep reasoning for complex tasks.

### Phase 3: Creativity (Weeks 7-9)
| Task | Priority | Effort |
|------|----------|--------|
| Add exploration_budget to dispatch | P2 | Medium |
| Implement `analogy_search` tool | P2 | Medium |
| Implement `synthesize_solutions` tool | P2 | Medium |

**Milestone**: Agent tries multiple approaches and learns from past solutions.

### Phase 4: Polish (Weeks 10-11)
| Task | Priority | Effort |
|------|----------|--------|
| Dynamic tool composition | P3 | Medium |
| Documentation | - | Low |
| Eval tests for new capabilities | - | Medium |

**Milestone**: Complete system with full capability set.

---

## Backwards Compatibility

All recommendations preserve backwards compatibility:

1. **Existing skills unchanged** - No modifications to SKILL.md format required; skill-builder continues to work as before
2. **New tools are additive** - Coordinator instruction updates are backward-compatible
3. **Escalation is optional** - Routing model remains default; escalation only when triggered
4. **Exploration budget defaults to 0** - Deterministic behavior preserved for existing dispatch calls
5. **Composable tools are opt-in** - Only skills with `composable_tools` field can request elevation

---

## Verification Checklist

After implementation, verify:

**Skill Acquisition:**
- [ ] Coordinator detects capability gaps and initiates acquisition workflow
- [ ] Researcher provides useful findings for skill creation
- [ ] skill-builder creates valid skills from coordinator-provided specifications
- [ ] `reload_skills` tool triggers hot-reload successfully

**Reasoning & World Model:**
- [ ] `reasoning_escalate` produces multi-approach analysis
- [ ] World Model tracks tool calls, files modified, error streaks
- [ ] World Model correctly resets between sessions (ephemeral)
- [ ] World Model `ShouldPivot()` triggers after consecutive failures
- [ ] World Model can query Epistemic Memory for preferences
- [ ] Epistemic Memory remains unaffected by World Model state

**Planning & Creativity:**
- [ ] `plan_deliberate` generates distinct approaches with trade-offs
- [ ] `analogy_search` returns relevant past solutions from memory
- [ ] `synthesize_solutions` combines partial results coherently

**Research:**
- [ ] researcher skill produces cited, multi-source reports
- [ ] `web_search` returns relevant results from configured backend

**Other:**
- [ ] Tool composition audit trail captures all elevations
- [ ] All existing eval tests continue to pass

---

## Summary

These recommendations address the core gaps between Kaggen's stated vision and current implementation:

| Gap | Recommendation | Transform |
|-----|---------------|-----------|
| Passive skill acquisition | Autonomous acquisition workflow | Agent detects gaps, researches, dispatches skill-builder |
| Routing not reasoning | Tiered architecture + World Model | Deep reasoning with execution-aware decisions |
| Operational planning | `plan_deliberate` | Strategic evaluation before action |
| No creativity | Exploration + analogy | Multiple approaches, learn from past |
| Peripheral research | First-class researcher | Research is core capability |
| Tool compartments | Dynamic composition | Novel tool combinations possible |

### Architectural Note: World Model vs. Epistemic Memory

A key design decision in these recommendations is maintaining **two distinct systems** for context:

- **Epistemic Memory** (existing): Long-term knowledge about the user—facts, preferences, experiences, observations. Persistent across sessions. Searched when relevant.

- **World Model** (new): Short-term execution state—files modified, tool call history, error streaks, task progress. Session-scoped. Consulted constantly.

These systems serve different purposes and must not be merged:
- Different lifecycles (persistent vs. ephemeral)
- Different access patterns (search vs. lookup)
- Different data models (unstructured recall vs. structured state)

They collaborate: World Model can query Epistemic Memory for user preferences; Epistemic Memory can observe World Model patterns across sessions to create new observations.

The result: Kaggen becomes a creative reasoning system capable of acquiring its own capabilities, with both long-term knowledge (epistemic) and short-term awareness (world model) informing its decisions.

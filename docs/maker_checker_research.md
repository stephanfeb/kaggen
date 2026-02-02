# Agentic Bot Architecture: From Coding to Personal Assistant

## Conversation Summary

This document captures a discussion about evolving an agentic coding bot into a more general-purpose personal assistant. The starting point was a working system with a declarative pipeline architecture and coordinator pattern, plus existing infrastructure for epistemic memory, user preferences, and cron scheduling.

---

## Part 1: Orchestration Models for Agentic Systems

### Current Architecture: Declarative Pipeline with Coordinator

A central coordinator drives a predefined workflow, routing tasks between specialized agents (e.g., coding agent, QA agent) and handling error recovery by looping work back when needed.

**Strengths:** Predictable flow, easy to reason about, works well when workflow shape is known.

**Limitations:** Can become a bottleneck for complex projects; routing logic may grow unwieldy.

### Alternative Orchestration Models

| Model | Description | Best For | Trade-offs |
|-------|-------------|----------|------------|
| **Hierarchical Multi-Agent** | Tree of supervisors; planning agent spawns sub-coordinators for different domains | Complex projects with distinct workstreams | More architectural complexity |
| **Blackboard Architecture** | Shared workspace; agents opportunistically pick up work they can contribute to | Emergent, collaborative problem-solving | Harder to debug and trace |
| **Event-Driven/Reactive** | Agents emit events, others subscribe and react | Decoupled systems, async workflows | Flow harder to follow end-to-end |
| **Plan-and-Execute** | Planner creates full task graph upfront, executor runs it, re-plan on significant failure | Tasks with clear decomposition | Less adaptive mid-execution |
| **Pure LLM Routing** | LLM decides on every turn which agent/tool to invoke | Maximum flexibility, open-ended tasks | Token-heavy, hard to test, unpredictable |

### Implementation Strategies by Model

**Hierarchical Multi-Agent:**
- Define domain boundaries (e.g., frontend vs backend, email vs calendar)
- Each sub-coordinator owns its agent pool and local state
- Parent coordinator handles cross-domain coordination and escalation
- Communication via structured messages up/down the hierarchy

**Blackboard Architecture:**
- Central data store with typed "facts" or "artifacts"
- Agents register patterns they can act on
- Conflict resolution strategy needed (priority, timestamps, or coordinator arbitration)
- Good for brainstorming/synthesis tasks where contribution order doesn't matter

**Event-Driven/Reactive:**
- Define event schema (e.g., `task_completed`, `test_failed`, `approval_received`)
- Agents declare subscriptions at registration
- Event bus or pub/sub infrastructure
- Consider event sourcing for replay and debugging

**Plan-and-Execute:**
- Planner agent produces DAG of tasks with dependencies
- Executor walks the DAG, parallelizing where possible
- Define re-planning triggers (failure threshold, new information, user interrupt)
- Keep plan artifacts for inspection

**Pure LLM Routing:**
- Single prompt that includes available agents/tools and current context
- LLM returns next action selection
- Wrap in retry/fallback logic
- Use for exploratory phases, then hand off to structured pipelines

### Practical Recommendation

The declarative pipeline scales well for coding bots because coding workflows are fairly structured. Stick with orchestrator patterns until hitting concrete pain points like deep parallelism needs, highly dynamic task decomposition, or genuine agent collaboration requirements.

---

## Part 2: Expanding to Personal Assistant Use Cases

### Key Differences from Coding Work

| Dimension | Coding Domain | Personal Assistant Domain |
|-----------|---------------|---------------------------|
| Success criteria | Tests pass/fail | Fuzzy ("good trip", "stay on top of things") |
| Artifacts | Text files | Emails, events, purchases, messages |
| Feedback loop | Tight, automated | Loose, human-judged |
| Reversibility | Git reset, delete file | Often irreversible |
| External state | Mostly self-contained | Calendars, inboxes, third-party services |
| Time dimension | On-demand | Deadlines, reminders, triggers |

### High-Value Personal Assistant Use Cases

1. **Information Synthesis**
   - Summarize inbox, Slack channels, document changes
   - Read-only, low risk, high value
   - *Implementation:* Integration adapters + summarization pipeline

2. **Scheduling Coordination**
   - Find mutually available times, propose meetings
   - Requires calendar read/write, possibly email for outreach
   - *Implementation:* Calendar API adapter, constraint solver, proposal system

3. **Research and Decision Support**
   - Compare options given constraints (purchases, vendors, tools)
   - Web search, structured comparison, personalized ranking
   - *Implementation:* Search tools + evaluation framework informed by user preferences

4. **Task Tracking and Nudging**
   - Persistent reminders, conditional follow-ups
   - State across sessions, time-triggered actions
   - *Implementation:* Memory layer + cron scheduling (already in place)

5. **Document and Content Preparation**
   - Draft emails, reports, responses
   - Closer to coding domain in structure
   - *Implementation:* Existing pipeline with output targeting different formats

---

## Part 3: Authorization Gates and Maker/Checker Pattern

### The Missing Piece

With memory, preferences, and scheduling already built, the critical gap for consequential actions is an approval layer that gates execution based on risk.

### Risk Tier Classification

| Tier | Examples | Execution Policy |
|------|----------|------------------|
| **Read-only / Reversible** | Search, summarize, draft to scratchpad | Auto-execute |
| **Low-stakes Mutation** | Create calendar event, add reminder | Auto-execute with notification, or quick confirm |
| **Consequential** | Send email, post content, make booking | Always propose-then-confirm |
| **High-stakes / Irreversible** | Delete data, financial transactions, actions affecting third parties | Confirm with explicit friction |

### Proposal Object Schema

```json
{
  "id": "proposal-uuid",
  "action": "send_email",
  "parameters": {
    "to": "sarah@example.com",
    "subject": "Following up on Thursday",
    "body": "..."
  },
  "rationale": "You asked me to follow up if no reply by today",
  "risk_tier": "consequential",
  "created_at": "2024-01-15T10:00:00Z",
  "expires_at": "2024-01-15T18:00:00Z",
  "status": "pending"
}
```

### Implementation Strategy

**1. Proposal Generation**
- Agents emit proposal objects instead of executing gated actions directly
- Proposals include action details, rationale, and risk classification
- Store proposals in persistent queue

**2. Approval Interface Options**
- *Synchronous:* Bot asks in conversation, waits for yes/no
- *Asynchronous:* Proposals queue up, user reviews batch via UI or summary
- *Hybrid:* Sync for interactive sessions, async otherwise

**3. Delegation Rules (Progressive Trust)**
- Allow users to define auto-approval rules:
  - "Always approve calendar events tagged 'routine'"
  - "Auto-send reply emails under 100 words"
- Rules stored in preferences, evaluated before prompting

**4. Execution and Audit**
- On approval: execute action, log result
- On rejection: log reason, optionally feed back to agent for learning
- On expiry: mark expired, notify if configured
- Full audit trail: proposal → decision → outcome

**5. Pipeline Integration**

Two approaches for handling the blocking nature of approvals:

*Pause-and-Resume:*
- Pipeline execution suspends at approval gate
- Resumes in same context when approval received
- Requires persistent pipeline state

*Complete-and-Trigger:*
- Pipeline completes with "pending_approval" status
- Separate process (cron) polls for approved proposals
- New pipeline run executes approved action
- Cleaner separation, works with existing cron infrastructure

### Recommended First Implementation

Start with email sending as the first gated consequential action:
- Clear risk/benefit (emails matter, mistakes are embarrassing)
- Well-defined action structure
- Natural place to test the full proposal→approval→execution flow
- Builds user trust before enabling higher-stakes actions

---

## Architecture Summary

```
┌─────────────────────────────────────────────────────────────────┐
│                        User Interface                           │
│                  (Conversation / Approval UI)                   │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Coordinator                             │
│            (Routes tasks, handles approval flow)                │
└─────────────────────────────────────────────────────────────────┘
                                │
        ┌───────────────────────┼───────────────────────┐
        ▼                       ▼                       ▼
┌───────────────┐       ┌───────────────┐       ┌───────────────┐
│  Task Agents  │       │ Integration   │       │   Approval    │
│  (Research,   │       │   Adapters    │       │    Engine     │
│   Drafting,   │       │  (Calendar,   │       │  (Risk tier,  │
│   Planning)   │       │   Email, Web) │       │   proposals,  │
└───────────────┘       └───────────────┘       │   delegation) │
        │                       │               └───────────────┘
        └───────────────────────┼───────────────────────┘
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Foundation Layer                           │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐             │
│  │  Epistemic  │  │    User     │  │    Cron     │             │
│  │   Memory    │  │ Preferences │  │  Scheduler  │             │
│  └─────────────┘  └─────────────┘  └─────────────┘             │
└─────────────────────────────────────────────────────────────────┘
```

---

## Next Steps

1. **Design proposal schema** — Define the structure for your domain
2. **Implement approval engine** — Risk classification + proposal storage
3. **Add approval interface** — Start with sync in-conversation approval
4. **Build first integration adapter** — Email is a good candidate
5. **Wire up execution path** — Cron polls approved proposals, triggers execution
6. **Add audit logging** — Every proposal, decision, and outcome
7. **Iterate on delegation rules** — Let trust build over time

---

*Document generated from conversation on agentic bot architecture expansion.*

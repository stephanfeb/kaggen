# Agent-to-Agent P2P Communication

This document captures design ideas for peer-to-peer communication between autonomous agents using libp2p with DHT and GossipSub.

> **Note:** This is distinct from client-to-server P2P (see [P2P Integration Guide](p2p-integration-guide.md)). This document focuses on **agent mesh** scenarios where multiple agents discover each other and coordinate work.

## Table of Contents

- [Overview](#overview)
- [Identity & Discovery](#identity--discovery)
- [Communication Patterns](#communication-patterns)
- [Protocol Design Questions](#protocol-design-questions)
- [Strawman Architecture](#strawman-architecture)
- [Use Cases](#use-cases)
- [Open Questions](#open-questions)

---

## Overview

### Why Agent-to-Agent Comms?

Traditional agent architectures use a central orchestrator. P2P agent communication enables:

- **Decentralized swarms** - No single point of failure
- **Specialist discovery** - Agents find experts dynamically
- **Parallel coordination** - Agents work together on decomposed tasks
- **Resilient workflows** - Tasks survive individual agent failures

### Key Primitives (libp2p)

| Primitive | Purpose |
|-----------|---------|
| **PeerID** | Cryptographic identity (derived from public key) |
| **DHT** | Distributed discovery and routing |
| **GossipSub** | Pub/sub for broadcasts and topic-based coordination |
| **Streams** | Direct multiplexed connections |

---

## Identity & Discovery

### PeerID as Agent Identity

Each agent has a keypair that derives its PeerID:

```
Agent A: 12D3KooWA...
Agent B: 12D3KooWB...
Agent C: 12D3KooWC...
```

The PeerID serves as both identity and addressing - other agents can route messages to it via the DHT.

### Capability Registry (DHT)

Agents can advertise capabilities via DHT providers:

```go
// Agent publishes its capabilities
dht.Provide(ctx, "/capabilities/code-review")
dht.Provide(ctx, "/capabilities/testing")
dht.Provide(ctx, "/capabilities/documentation")

// Other agents discover specialists
reviewers := dht.FindProviders(ctx, "/capabilities/code-review")
```

### Rendezvous for Specializations

Using rendezvous strings for agent "guilds":

```go
// Join a specialization group
rendezvous.Register(ctx, "agents/frontend-specialists")
rendezvous.Register(ctx, "agents/security-auditors")

// Find agents in a group
peers := rendezvous.Discover(ctx, "agents/frontend-specialists")
```

### Discovery Approaches

| Approach | Mechanism | Trade-offs |
|----------|-----------|------------|
| **Push** | Agents announce to known topics via GossipSub | Real-time, but floods network |
| **Pull** | Query DHT for capabilities on-demand | Efficient, but latency on first lookup |
| **Hybrid** | GossipSub for presence, DHT for capability details | Best of both |

---

## Communication Patterns

### 1:1 Request/Response

Direct stream for task delegation and queries:

```
Agent A                              Agent B
   │                                    │
   │─────── TaskRequest ───────────────►│
   │                                    │ (processes)
   │◄────── TaskResponse ──────────────│
   │                                    │
```

**Use cases:**
- Delegating a subtask to a specialist
- Querying for information
- Requesting code review

### Broadcast (GossipSub)

For announcements visible to all interested agents:

```
Agent A publishes to /agents/announcements
        │
        ├──► Agent B (subscribed)
        ├──► Agent C (subscribed)
        └──► Agent D (subscribed)
```

**Use cases:**
- Status updates ("I completed task X")
- Availability changes ("I'm going offline")
- Work auctions ("Task X needs an owner")

### Topic-Based Coordination

GossipSub topics for project/task coordination:

```go
// All agents working on a feature
topic := gossipsub.Join("/projects/user-auth-refactor")

// Coordinate via pub/sub
topic.Publish(StatusUpdate{Agent: myID, Status: "completed login module"})

// Listen for updates
sub, _ := topic.Subscribe()
for msg := range sub.Messages() {
    handleTeamUpdate(msg)
}
```

### Persistent Channels

Long-running multiplexed streams for ongoing collaboration:

```go
// Open persistent channel for collaboration
stream, _ := host.NewStream(ctx, partnerPeerID, "/agent/collab/1.0.0")

// Bidirectional communication over time
go readLoop(stream)
go writeLoop(stream)
```

---

## Protocol Design Questions

### Message Format

Options for agent-to-agent message encoding:

| Format | Pros | Cons |
|--------|------|------|
| **JSON-RPC** | Human-readable, familiar | Verbose, parsing overhead |
| **Protobuf** | Compact, typed, fast | Requires schema management |
| **CBOR** | Compact, schema-less | Less tooling than JSON |
| **Custom** | Optimized for agent needs | Maintenance burden |

**Proposed:** Protobuf for structured messages (tasks, results) with optional CBOR for arbitrary context.

### Conversational vs Stateless

**Stateless:** Each message is self-contained
```json
{
  "task_id": "abc123",
  "context": { ... full context ... },
  "request": "review this code"
}
```

**Conversational:** Messages carry conversation ID, context builds up
```json
{
  "conversation_id": "conv456",
  "in_reply_to": "msg789",
  "content": "I found an issue in line 42"
}
```

**Hybrid approach:** Task-level context with message threading within tasks.

### Trust & Authorization

PeerID verification is automatic via libp2p Noise handshake. But authorization needs more:

| Mechanism | Description |
|-----------|-------------|
| **Capability tokens** | Bearer tokens granting specific permissions |
| **Delegation chains** | Agent A authorizes B to act on its behalf |
| **Reputation** | Track agent reliability over time |
| **Allowlists** | Only accept tasks from known agents |

### Coordination Primitives

How agents decide who does what:

| Primitive | Description | Use Case |
|-----------|-------------|----------|
| **First-come** | First to claim gets the task | Simple workloads |
| **Auction/Bidding** | Agents bid on tasks | Optimize for capability |
| **Consensus** | Multi-agent agreement | Critical decisions |
| **Leader election** | Dynamic coordinator | Hierarchical workflows |

---

## Strawman Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Agent Application Layer                   │
│  (task management, reasoning, skill execution)              │
├─────────────────────────────────────────────────────────────┤
│                    Agent Protocol Layer                      │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────────────────┐│
│  │   Task      │ │   Query     │ │     Coordination        ││
│  │  Protocol   │ │  Protocol   │ │      Protocol           ││
│  │ (delegate,  │ │ (ask, tell, │ │ (join team, sync state, ││
│  │  report)    │ │  search)    │ │  vote, elect leader)    ││
│  └─────────────┘ └─────────────┘ └─────────────────────────┘│
├─────────────────────────────────────────────────────────────┤
│          GossipSub (topics)      │     Direct Streams       │
│  ┌─────────────────────────────┐ │ ┌─────────────────────┐  │
│  │ /agents/announce            │ │ │ /agent/task/1.0.0   │  │
│  │ /projects/<id>              │ │ │ /agent/query/1.0.0  │  │
│  │ /tasks/available            │ │ │ /agent/collab/1.0.0 │  │
│  └─────────────────────────────┘ │ └─────────────────────┘  │
├─────────────────────────────────────────────────────────────┤
│                    DHT (Kademlia)                            │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ Capability Registry: /capabilities/<skill> → [PeerIDs] ││
│  │ Agent Metadata: /agents/<peerID> → AgentProfile        ││
│  │ Task State: /tasks/<taskID> → TaskStatus               ││
│  └─────────────────────────────────────────────────────────┘│
├─────────────────────────────────────────────────────────────┤
│                 libp2p Foundation                            │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌───────────┐ │
│  │  Identity  │ │ Transport  │ │  Security  │ │   Muxer   │ │
│  │  (PeerID)  │ │(QUIC, TCP) │ │  (Noise)   │ │  (yamux)  │ │
│  └────────────┘ └────────────┘ └────────────┘ └───────────┘ │
└─────────────────────────────────────────────────────────────┘
```

---

## Use Cases

### Task Delegation

Agent A needs code review, finds a specialist:

```
1. Agent A: dht.FindProviders("/capabilities/code-review")
2. Agent A: Opens stream to Agent B (specialist)
3. Agent A → B: TaskRequest{type: "review", code: "...", context: "..."}
4. Agent B: Performs review
5. Agent B → A: TaskResponse{findings: [...], approved: false}
```

### Swarm Coordination

Multiple agents decompose and parallelize work:

```
1. Coordinator publishes to /projects/refactor-auth
2. Workers subscribe, receive task decomposition
3. Each worker claims subtasks via auction
4. Workers publish progress to topic
5. Coordinator aggregates results
```

### Shared Context/Memory

Agents maintain shared knowledge:

```go
// Publish to shared context topic
topic.Publish(ContextUpdate{
    Key:   "project-decisions",
    Value: "Decided to use JWT, not sessions",
    Agent: myID,
})

// DHT for persistent storage
dht.PutValue(ctx, "/context/project-123/decisions", decisions)
```

### Specialist Consultation

Agent encounters unfamiliar domain, finds expert:

```
1. Agent A: Encounters security question
2. Agent A: dht.FindProviders("/capabilities/security-audit")
3. Agent A → Expert: QueryRequest{question: "Is this SQL safe?", code: "..."}
4. Expert → A: QueryResponse{answer: "No, SQL injection at line 5", fix: "..."}
```

---

## Open Questions

### Discovery & Presence

- How do agents announce presence on startup?
- How long before an agent is considered offline?
- Should we use DHT expiry or explicit "going offline" messages?

### Task Lifecycle

- Who owns task state - the task creator, the executor, or shared?
- How are task failures handled - retry, reassign, escalate?
- What's the timeout model for unresponsive agents?

### Context & Memory

- How much context should travel with each message?
- Should agents maintain local memory of peer interactions?
- How do we prevent context from growing unboundedly?

### Scale

- How many agents can effectively coordinate?
- What's the gossip overhead at 10, 100, 1000 agents?
- Do we need hierarchical organization at scale?

### Security

- How do we prevent malicious agents from poisoning the swarm?
- What's the trust model for new agents joining?
- How do we handle capability impersonation?

---

## Next Steps

1. **Define core protocols** - Task, Query, Coordination message schemas
2. **Prototype discovery** - Capability registry with DHT
3. **Build simple swarm** - 2-3 agents coordinating on a task
4. **Measure overhead** - GossipSub at various scales
5. **Design trust model** - Authorization and reputation

---

## Related Documents

- [P2P Integration Guide](p2p-integration-guide.md) - Client-to-server P2P (different use case)
- [Complex Task Orchestration](COMPLEX_TASK_ORCHESTRATION.md) - Task decomposition patterns
- [Architecture](ARCHITECTURE.md) - Overall kaggen architecture

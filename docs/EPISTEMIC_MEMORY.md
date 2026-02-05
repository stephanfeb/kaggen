# Epistemic Memory

Kaggen's memory system is inspired by the [Hindsight](https://arxiv.org/abs/2512.12818) paper on epistemic memory for AI agents. The core idea: not all memories are equal. A name is different from a preference, which is different from something that happened. Kaggen's memory understands these differences and uses them to recall the right thing at the right time.

## The four kinds of memory

Every memory Kaggen stores is classified as one of four types:

| Type | What it means | Example |
|------|---------------|---------|
| **Fact** | Something objectively true | "User works as a software engineer" |
| **Experience** | Something that happened | "User relocated to Berlin in January 2025" |
| **Opinion** | A preference or belief | "User prefers Go over Rust for CLI tools" |
| **Observation** | A pattern Kaggen noticed | "User tends to discuss work topics on weekday mornings" |

Facts, experiences, and opinions come from conversations. Observations are different -- Kaggen generates them on its own by looking across many memories and spotting patterns (more on this below).

## How memories are created

After each conversation, a background process reviews what was said and decides whether anything is worth remembering. When it finds something, it does three things:

```
Conversation
    |
    v
+---------------------+
|  Memory Extractor   |  Reads the conversation, decides what to remember
+---------------------+
    |
    |  For each new memory:
    |
    v
+---------------------+
|  1. Classify        |  Is this a fact, experience, or opinion?
|  2. Tag             |  Who/what is mentioned? When did it happen?
|  3. Rate            |  How confident are we? (0 to 1)
+---------------------+
    |
    v
+---------------------+
|     Store           |  Save to database with all metadata
+---------------------+
```

A memory like "User prefers Go over Rust" gets stored with:
- **Type:** opinion
- **Confidence:** 0.8 (stated clearly, but not emphatically)
- **Entities:** Go, Rust
- **Topics:** preferences, programming

## Confidence and opinion evolution

Opinions change over time. If the user mentions the same preference again, confidence goes up. If they say something contradictory, it goes down. Kaggen uses a smoothing formula so that confidence shifts gradually rather than flipping back and forth:

```
         +1.0 |-----------..........
              |         ..
              |       .
              |     .       <-- Confidence rises as the user
              |   .             keeps mentioning the same thing
              |  .
         +0.5 |.
              |
              +---+---+---+---+---+---
                  1   2   3   4   5   (mentions over time)
```

This means a single offhand remark won't erase a well-established opinion.

## The entity graph

Kaggen doesn't just remember text -- it builds a web of connections between the people, places, and things mentioned in memories.

```
              Berlin  ----  Germany
               /               \
              /                 \
         User lives         User visited
              \                 /
               \               /
            Software Eng. -- Go -- Rust
                               \
                                CLI tools
```

Every time two entities appear in the same memory, the connection between them gets stronger. This web of relationships lets Kaggen answer questions it was never directly told the answer to -- for example, asking about "Germany" can surface the memory about living in Berlin, even if the user never said "Germany" in that conversation.

## Four-way recall

When Kaggen searches for relevant memories, it doesn't rely on a single method. It runs four searches in parallel and combines the results:

```
                        "What did I do in Berlin?"
                                  |
                 +----------------+----------------+
                 |                |                |                |
                 v                v                v                v
           +---------+     +---------+     +---------+     +---------+
           | Meaning |     | Keyword |     |  Graph  |     |  Time   |
           +---------+     +---------+     +---------+     +---------+
           | Finds     |   | Finds     |   | Finds     |   | Finds     |
           | memories  |   | memories  |   | memories  |   | memories  |
           | with      |   | containing|   | connected |   | from the  |
           | similar   |   | the exact |   | through   |   | right     |
           | meaning   |   | words     |   | entities  |   | time      |
           +---------+     +---------+     +---------+     +---------+
                 |                |                |                |
                 +-------+--------+--------+-------+
                         |
                         v
                  +--------------+
                  |    Merge     |  Combine rankings from all four
                  +--------------+  channels using Reciprocal Rank Fusion
                         |
                         v
                  Best memories, ranked
```

1. **Meaning search** -- uses vector embeddings to find memories that are semantically similar, even if they use different words.
2. **Keyword search** -- finds memories containing the exact words in the query. Good for names and specific terms.
3. **Graph search** -- starts from entities mentioned in the query and walks the connection graph to find related memories. This is what connects "Berlin" to "Germany" to "relocation".
4. **Time search** -- parses temporal expressions like "last month" or "in 2025" and finds memories from that period.

The results from all four channels are merged using a technique called Reciprocal Rank Fusion, which gives higher weight to memories that appear in multiple channels.

## Background observation synthesis

Periodically, Kaggen reviews entities that have accumulated enough memories (at least three) and asks itself: "What do I know about this?" The answer becomes a new observation-type memory.

```
Memories about "Go":
  - User prefers Go over Rust for CLI tools
  - User works as a software engineer
  - User built a project in Go last month

        |
        v  (Synthesis)

Observation:
  "The user is a software engineer who actively uses Go for
   projects and prefers it over Rust for command-line tooling."
```

These observations participate in search like any other memory, so Kaggen can recall synthesized knowledge even when no single memory contains the full picture.

## How it all fits together

```
     Conversation
          |
          v
    +------------------+
    |   Extraction     |  Classify, tag, rate
    +------------------+
          |
    +-----+------+
    |            |
    v            v
 +--------+  +--------+
 |Memories|  |Entities|
 |  DB    |<>| Graph  |
 +--------+  +--------+
    |            |
    |     +------+
    |     |
    v     v
 +-----------+         +---------------+
 | 4-Way     | <-----> | Background    |
 | Recall    |         | Synthesis     |
 +-----------+         +---------------+
       |                      |
       v                      v
  Relevant memories     New observations
  for the current       added to the
  conversation          memory DB
```

The system forms a virtuous cycle: conversations create memories, memories build the entity graph, the entity graph enables richer recall, and background synthesis creates new observations that make future recall even better.

## Memory preservation during compaction

When conversation context grows too large and needs to be compacted (summarized), Kaggen ensures memories are extracted first:

```
Session context fills up
        |
        v
+------------------+
|  /compact called |
+------------------+
        |
        v
+------------------+
| BeforeCompaction |  Extract memories from events about to be deleted
+------------------+
        |
        v
+------------------+
|  LLM Summarizes  |  Older events summarized into text
+------------------+
        |
        v
+------------------+
|  Events Pruned   |  Old events removed, summary persisted
+------------------+
```

This "memory flush before compaction" ensures that preferences, facts, and experiences mentioned in older conversation turns are captured in the memory database before those turns are removed from the session. Without this, rapid conversation or slow extraction could cause memory loss.

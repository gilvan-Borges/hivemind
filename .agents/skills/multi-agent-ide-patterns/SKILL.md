---
name: multi-agent-ide-patterns
description: >-
  Use this skill when designing or extending multi-agent orchestration features, agent composer UX,
  context isolation, shared memory, human-in-the-loop plan gates, or token telemetry in HiveMind.
  Synthesizes muratcankoylan/Agent-Skills-for-Context-Engineering (multi-agent-patterns) and Cursor 2.0 multi-agent UX.
---

# Multi-Agent IDE Orchestration & UX Patterns

## 1. Explicit Execution Modes (Token Economics vs Depth)

Multi-agent runs consume significantly more tokens than single-agent turns. Expose explicit execution modes in the Composer so the developer controls cost, latency, and autonomy:

1. **`auto` (Smart Triage)**: Coordinator (`Chefe`) inspects workspace/images read-only and either answers directly (1x token cost) or slices non-overlapping tasks across parallel `Dev` worktrees.
2. **`direct` (Single-Agent Fast Pass)**: Forces 1-agent response without squad delegation — ideal for architectural Q&A, log analysis, and code explanation.
3. **`plan` (Human Checkpoint Gate)**: Coordinator inspects the codebase and produces an explicit plan + per-developer file slices, pausing for human review with a 1-click **"Aprovar & Executar Squad"** action before spawning worktrees.
4. **`squad` (Full Parallel Pipeline)**: Forces `Chefe → Parallel Devs (isolated Git worktrees) → Adversarial QA Loop → Closing Chief` synthesis.

## 2. Context Isolation & Shared Filesystem Memory

- **Prevent the Telephone Game**: Pass structured state keys (`chief_plan`, per-dev exclusive file slices, `qa_feedback`) rather than dumping noisy raw transcripts across sub-agents.
- **Shared Persistent Memory (`.hivemind/MEMORY.md`)**: Store durable project conventions, build quirks, and architectural decisions in `.hivemind/MEMORY.md` so every run inherits concise project memory without repeating instructions in chat.
- **Conflict-Free Parallelism**: Every parallel `Dev` agent operates in its own Git worktree and receives an explicit file boundary slice (`dev_tasks[i]`).

## 3. Observable Swarm Mission Control

Because the bottleneck in multi-agent coding shifts from *writing* code to *supervising and reviewing* parallel agents:

- **Mission Control Board**: Provide a real-time multi-agent view displaying each agent's role, live status, active tool call, inline model selector (`agy` models), call count, and `%` share of session token consumption.
- **Adversarial QA Visibility**: Surface `qa_request_changes` and `qa_approve` handoffs as structured cards, followed by an actionable diff summary (`Commitar` / `Descartar`).

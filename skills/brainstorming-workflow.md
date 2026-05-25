---
name: brainstorming-workflow
description: >
  Use when an agent is in an interactive session and the user requests a new feature
  or significant change. Provides a structured process choice before any code is written.
  Skip entirely when dispatched with a complete task by another agent or automation.
---

# Brainstorming Workflow

Discipline for interactive sessions involving new features or significant changes.

## Context Detection

- **Interactive session** (human in CLI/IDE): follow this workflow.
- **Dispatched with a complete task** (sub-agent, automation, explicit spec): skip entirely and execute.

## First Message

Before writing any code, ask exactly ONE question:

> I can approach this a few ways:
>
> A) Jump straight to coding
> B) Discuss approaches first, then code
> C) Full design process — explore approaches, write a spec, then an implementation plan before coding
>
> Which works for you?

If the human says "just do it", gives a direct instruction, or otherwise signals urgency, treat as **A**.

## Path A — Jump to Coding

Proceed directly. All existing rules still apply (pre-commit verification, coverage,
CLAUDE.md standards). No additional ceremony.

## Path B — Discuss Approaches

1. **Understand the problem**: what is being solved, why, and any constraints.
2. **Propose 2–3 approaches** with trade-offs (complexity, risk, performance, maintainability).
3. **Lead with a recommendation** and explain why.
4. Let the human choose, then code.

Ask one question at a time. Prefer multiple choice over open-ended questions.

## Path C — Full Design Process

Everything in Path B, plus:

1. **Write a design spec**. Ask the human where they want to save it to.
2. **Break into an ordered implementation plan** with file paths and dependencies.
3. **Optionally dispatch sub-agents** for independent tasks if the plan supports parallelism.

## Key Principles

- **One question at a time.** Never pile up multiple questions in one message.
- **Prefer multiple choice.** Easier for the human to decide quickly.
- **Human decides the process, not the agent.** Respect the chosen path.
- **"Just do it" means just do it.** Don't add process the human didn't ask for.

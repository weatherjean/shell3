---
name: brainstorming
description: Use before any non-trivial feature, behavior change, or new component. Turns a rough idea into an agreed design through one-question-at-a-time dialogue, then writes a saved design doc.
---

# Brainstorming ideas into designs

## Hard gate

Do NOT write code, edit files, or start implementing until you have presented a design and the user has approved it. This applies to every task regardless of how simple it looks. "Too simple to need a design" is where unexamined assumptions cost the most. The design can be three sentences for a tiny change — but present it and get a yes first.

## Process

1. **Explore context first.** Read the relevant files, docs, and recent commits before asking anything. Ground your questions in what actually exists.
2. **Ask questions one at a time.** One question per message. Prefer multiple-choice when you can; open-ended is fine. Focus on purpose, constraints, and success criteria — not implementation trivia. Keep going until you understand what you are building.
3. **Scope check.** If the idea is really several independent subsystems, say so early and help split it into separate pieces, each with its own design. Do not refine details of something that should be decomposed first.
4. **Propose 2-3 approaches.** Lead with your recommendation and say why. Give the trade-offs honestly.
5. **Present the design in sections.** Scale each section to its complexity — a sentence or two when straightforward, more when nuanced. Cover architecture, components, data flow, error handling, and testing. Ask after each section whether it looks right before moving on.
6. **Design for isolation.** Break the system into small units, each with one clear purpose and a well-defined interface, understandable and testable on its own. For each unit you should be able to say what it does, how it is used, and what it depends on. When a file would grow large, that is a signal it is doing too much.
7. **Work with the grain of the codebase.** Follow existing patterns. Improve code you are already touching when it has problems that affect the work; do not go off on unrelated refactors.

## After approval

- Write the agreed design to `docs/specs/YYYY-MM-DD-<topic>.md` (or the project's conventional spec location). Be concrete: no "TBD", no contradictions, no requirement that could be read two ways.
- If the project is a git repo, commit the design doc.
- Then tell the user the design is saved at that path, and implement it once they agree.

## Key principles

- YAGNI ruthlessly — cut features that are not needed.
- Be willing to go back when something does not fit.

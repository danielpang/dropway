---
name: design-doc-template
description: Guides drafting a product/engineering design doc — problem statement, goals and non-goals, proposed design, alternatives considered, and rollout plan.
---

# Design doc template

Use this skill when asked to write a design document, an RFC, or a technical
proposal. Produce the document from `template.md`, following the guidance
below for each section.

## Section guidance

- **Problem statement** — Describe the user-visible or business problem, not
  the solution. A reader should finish this section agreeing the problem is
  worth solving before seeing any design.
- **Goals / non-goals** — Goals are testable outcomes ("p95 upload < 2s"), not
  activities. Non-goals prevent scope creep; list the tempting adjacent work
  this design deliberately excludes.
- **Proposed design** — Start with a two-paragraph overview a skimming
  executive can follow, then go deep: data model, API surface, failure modes.
  State every assumption you're making about existing systems.
- **Alternatives considered** — At least two real alternatives with the
  concrete reason each was rejected (cost, risk, timeline — not "worse").
  If no alternative was seriously considered, the design isn't ready.
- **Rollout** — Migration steps, feature flags, how to measure success, and
  the rollback story. A design that can't be rolled back needs a justification
  here.

## Quality bar

- Write for a reader who wasn't in any of the meetings.
- Every open question gets an owner and a deadline, or it's a decision.
- Keep it under ~2000 words; move detail to appendices.

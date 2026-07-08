---
name: pr-review-checklist
description: A structured checklist for reviewing pull requests — correctness, tests, security, and readability — with guidance on writing actionable review comments.
---

# PR review checklist

Use this skill when asked to review a pull request or a diff. Work through the
checklist in order and report findings grouped by severity.

## How to review

1. Read the PR description first. If the intent is unclear, say so — an
   unreviewable description is itself a finding.
2. Read the full diff before commenting on any single hunk; a "bug" in one file
   is often handled in another.
3. For each checklist area below, look for concrete, demonstrable problems.
   Prefer "this breaks when X" over "this could be cleaner".

## Checklist

### Correctness
- Does the change do what the description says?
- Off-by-one errors, inverted conditions, missed early returns.
- Error paths: are failures handled, propagated, or silently swallowed?
- Concurrency: shared state without synchronization, TOCTOU windows.

### Tests
- Is new behavior covered by a test that would fail without the change?
- Do edge cases from the checklist above have cases?
- Are tests asserting outcomes, not implementation details?

### Security
- Input validation at trust boundaries (user input, network, files).
- Authorization checks on every new endpoint or query (not just authentication).
- Secrets: nothing hard-coded, logged, or echoed back in errors.

### Readability & fit
- Names say what things are; comments say why, not what.
- The change follows the file's existing patterns rather than inventing new ones.
- Dead code, leftover debug output, and commented-out blocks are removed.

## Writing the review

- Lead with a one-paragraph summary: what the PR does and your overall verdict.
- Group findings: **blocking** (bugs, security), **should-fix** (tests, design),
  **nit** (style). Prefix each comment accordingly.
- For every blocking finding, describe the failure scenario: the input or state
  that triggers it and what goes wrong.
- If everything looks good, say so explicitly and call out one thing done well.

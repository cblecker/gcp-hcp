---
description: "Process open PR review comments and push fixes"
arguments: "[PR_NUMBER] [batch|loop] - PR number (optional) and optional execution mode"
user_invocable: true
---

Invoke the `author-code-review` agent using the Task tool with `subagent_type="author-code-review"`.

Pass the following context to the agent:
- PR number: Parse from $ARGUMENTS if provided, otherwise show all open PRs for selection
- Execution mode: "batch" for batch mode, "loop" for continuous mode, otherwise interactive

The agent will:

1. Find all my open PRs in this repository
2. Analyze review comments (including CoderabbitAI)
3. Identify actionable code change requests
4. Implement the requested changes
5. Run verification using repo-appropriate commands
6. Commit and push fixes

If execution mode is "batch", run in batch mode - do not ask for confirmation.
If execution mode is "loop", run in loop mode - continuously process until no actionable comments remain.
Otherwise, run in interactive mode - confirm with me before making changes and before pushing.

$ARGUMENTS

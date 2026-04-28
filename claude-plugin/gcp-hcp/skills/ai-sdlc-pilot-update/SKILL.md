---
name: ai-sdlc-pilot-update
description: Post a biweekly Agentic SDLC pilot update comment to GCP-579, gathering Jira and GitHub activity for the lookback period.
arguments: days
argument-hint: "[days]"
disable-model-invocation: true
model: sonnet
effort: high
---

# Agentic SDLC Pilot Update Skill

Gather data from Jira and GitHub, draft a biweekly update comment, get user input on subjective sections, and post the final comment to GCP-579.

**Tracking issue:** GCP-579 ("Establish AI-Native Developer Experience — Agentic SDLC Pilot")
**Deadline:** June 30, 2026

---

## Phase 1: Gather Jira Activity (BFS traversal)

Determine the lookback window. Default is 14 days. If `$days` was provided, use that value instead. If `$days` is missing, non-numeric, or less than 1, fall back to 14.

Compute:
- `end_date` = today's date
- `start_date` = today minus lookback days

**BFS traversal starting at GCP-579:**

Use a queue-based BFS. Start with `["GCP-579"]`.
Before traversal, fetch and store GCP-579's own fields using `mcp__atlassian__jira_get_issue` with `fields="key,summary,description,status,assignee,issuetype,created,updated"`.
For each key in the queue:
1. Search for direct children: `parent = KEY ORDER BY updated DESC`
   - Use `mcp__atlassian__jira_search` with `fields="key,summary,description,status,assignee,issuetype,created,updated"` and `limit=50`
   - Paginate with `start_at` if `total > 50` (pagination applies per parent-key search, not once globally)
2. Fetch each child's key, summary, description, status, assignee, issuetype, created, updated fields
3. Add each child's key to the queue
4. Continue until the queue is empty

**After collecting all keys in the hierarchy (including GCP-579 itself, with fields stored for each):**

Fetch changelogs for all discovered keys using `mcp__atlassian__jira_batch_get_changelogs` with `fields="status"` to get status transitions. Filter transitions to those whose timestamp falls within the lookback window.

**Classify findings:**

- **Completed issues**: status transitioned to "Done", "Closed", or "Resolved" within the window
- **Created issues**: `created` field is within the window
- **Status transitions**: any status change within the window (excluding completions)
- **Blocked issues**: status is "Blocked" or summary/description contains "blocked" (case-insensitive)

---

## Phase 2: Gather GitHub Activity

**Repos to search:**
- `openshift-online/gcp-hcp`
- `openshift-online/gcp-hcp-infra`
- `openshift-eng/ai-helpers`
- `openshift-online/gcp-hcp-priv` (may have restricted access; skip gracefully if unavailable)

For each repo, use `mcp__plugin_github_github__search_pull_requests` with these queries:

**Merged PRs (last N days):**
```text
repo:<owner>/<repo> is:pr is:merged merged:>={start_date}
```

**Open PRs (in progress):**
```text
repo:<owner>/<repo> is:pr is:open updated:>={start_date}
```

**Filter to pilot-relevant PRs** by checking if any of these apply:
- Has label `agentic-sdlc-pilot`
- Touches paths: `claude-plugin/`, `.claude/`, `CLAUDE.md`, `AGENTS.md`, skills, agents
- Title or body mentions: "SDLC", "pilot", "agentic", "skill", "claude", "AI"

If the repo `openshift-online/gcp-hcp` is the current working directory, optionally supplement with:
```bash
git log --since="{start_date}" --oneline --no-merges
```

Collect for each pilot-relevant PR: number, title, state, merged_at or created_at, repo name.

---

## Phase 3: Draft the Update

Assemble gathered data into the 5-section template below.

**Auto-populate "What We Tried"** from:
- New Jira issues created in the window (list as: `[KEY] Summary — issuetype`)
- PRs opened (not yet merged)
- Tools, workflows, or skills referenced in PR titles/descriptions/Jira summaries

**Auto-populate "What Happened"** from:
- PRs merged (list as: `[repo#N] Title`)
- Jira issues completed (list as: `[KEY] Summary`)
- Notable status transitions (e.g., moved from In Progress → Review)

**Leave placeholders** for the subjective sections and prompt the user:

```text
I've pre-populated "What We Tried" and "What Happened" from Jira and GitHub activity.
Please provide input for the remaining sections:

**What We Learned** — surprises, missing context, things to do differently next time:
> 

**What's Blocked** — tooling gaps, access issues, integration problems:
> 

**What We're Trying Next** — next experiments and focus areas for the coming two weeks:
> 
```

Collect the user's responses interactively. Use `AskUserQuestion` if needed, or prompt inline and wait for replies.

---

## Phase 4: Review and Confirm

Assemble the complete comment using the template:

```markdown
## Agentic SDLC Pilot Update — {start_date} to {end_date}

### What We Tried
{auto-populated bullet list: tools, workflows, issue types worked on, PRs opened}

### What Happened
{auto-populated bullet list: PRs merged, issues completed, status changes}

### What We Learned
{user input}

### What's Blocked
{user input}

### What We're Trying Next
{user input}
```

Display the full formatted Markdown in the terminal. Ask the user to confirm or request edits:

```text
Here is the full draft comment for GCP-579. Reply with:
- "post" to post as-is
- Specific edits to apply (e.g., "change the third bullet in What Happened to...")
- "cancel" to abort
```

Apply any requested edits and re-display until the user confirms with "post".

---

## Phase 5: Post the Comment

Post the confirmed comment to GCP-579:

```python
mcp__atlassian__jira_add_comment(
    issue_key="GCP-579",
    body=<final_markdown>
)
```

Confirm success with: "Comment posted to GCP-579."

If posting fails, display the full markdown so the user can copy-paste it manually.

---

## Error Handling

- **Repo not accessible** (e.g., `gcp-hcp-priv`): skip that repo, note it was skipped in the output.
- **No pilot-relevant PRs found**: include a note "No pilot-relevant PRs found in this period" in "What Happened".
- **BFS finds no children under GCP-579**: report only GCP-579 itself; this is valid.
- **Jira batch changelog limit**: if >20 keys, batch in groups of 20.
- **User provides empty input for a subjective section**: use "Nothing to report this period." as the placeholder.

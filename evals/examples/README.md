# Example runs

One run per scenario (Claude Opus 5.5, effort `medium`). The bad-commit runs
are from the Oct 17 eval; the drift runs are from the Oct 26 eval in the main
README.

| Scenario | Outcome | Terminal output | Audit log |
|---|---|---|---|
| Bad image tag | `RESOLVED`: reverted the bad commit | [bad-image-tag.log](bad-image-tag.log) | [bad-image-tag.jsonl](bad-image-tag.jsonl) |
| Crash loop | `RESOLVED`: reverted the bad commit | [crash-loop.log](crash-loop.log) | [crash-loop.jsonl](crash-loop.jsonl) |
| OOM | `RESOLVED`: reverted the bad commit | [oom.log](oom.log) | [oom.jsonl](oom.jsonl) |
| Drift from automation | `RESOLVED`: synced back to Git | [drift-automation.log](drift-automation.log) | [drift-automation.jsonl](drift-automation.jsonl) |
| A person's bad live change | `ESCALATED`: no action | [manual-bad-image.log](manual-bad-image.log) | [manual-bad-image.jsonl](manual-bad-image.jsonl) |
| A person's hotfix | `ESCALATED`: no action | [manual-hotfix.log](manual-hotfix.log) | [manual-hotfix.jsonl](manual-hotfix.jsonl) |

Each audit log line is one event:

| Event | Contents |
|---|---|
| `start` | The alert, model, effort, dry-run flag, allowed actions, action limit |
| `llm_response` | Claude's full response for that step: summarized reasoning, text, tool calls |
| `tool_call` | Tool name, input, full output, and whether it errored |
| `guardrail_allowed` / `guardrail_denied` / `action_rejected` | The guardrail decision for an action, with the plan or the reason |
| `action_applied` | What was changed and the revision the app should end up at |
| `action_verified` | Whether recovery was confirmed, with the final ArgoCD status |
| `diagnosis` | The structured `submit_diagnosis` result |
| `outcome` | The final status (`resolved` / `proposed` / `escalated`) and why |
| `end` | Token usage |

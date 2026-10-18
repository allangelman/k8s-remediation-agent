# Example runs

One run per scenario from the eval in the main README (Claude Opus 5.5, effort
`medium`). All three ended `RESOLVED`.

| Scenario | Terminal output | Audit log |
|---|---|---|
| Bad image tag | [bad-image-tag.log](bad-image-tag.log) | [bad-image-tag.jsonl](bad-image-tag.jsonl) |
| Crash loop | [crash-loop.log](crash-loop.log) | [crash-loop.jsonl](crash-loop.jsonl) |
| OOM | [oom.log](oom.log) | [oom.jsonl](oom.jsonl) |

Each audit log line is one event:

| Event | Contents |
|---|---|
| `start` | The alert, model, effort, dry-run flag, allowed actions, action limit |
| `llm_response` | Claude's full response for that step: summarized reasoning, text, tool calls |
| `tool_call` | Tool name, input, full output, and whether it errored |
| `guardrail_allowed` / `guardrail_denied` / `action_rejected` | The guardrail decision for an action, with the plan or the reason |
| `action_applied` | What was changed and the commit pushed |
| `action_verified` | Whether recovery was confirmed, with the final ArgoCD status |
| `diagnosis` | The structured `submit_diagnosis` result |
| `outcome` | The final status (`resolved` / `proposed` / `escalated`) and why |
| `end` | Token usage |

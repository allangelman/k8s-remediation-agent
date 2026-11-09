package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool results longer than this are cut before being sent to the model (the
// audit log keeps the full output). Events and describe output are usually a
// few KB; this only guards against something unexpectedly huge.
const maxToolOutput = 20000

// Run handles one incident and returns its outcome. This is the agent loop:
// send the conversation to Claude, run whatever tools it asks for (passing
// actions through the guardrails), send the results back, and repeat until it
// calls submit_diagnosis. The returned error is only for infrastructure
// failures (API down, kubectl missing); a run that can't fix the app returns
// an escalated Outcome.
func Run(ctx context.Context, cfg Config, client anthropic.Client, audit *AuditLog, out io.Writer) (*Outcome, error) {
	// Observe: start from what ArgoCD reports, the same signal an alert would carry.
	stage(out, "OBSERVE", "reading ArgoCD status for %q", cfg.App)
	status, err := cfg.GetAppStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("read app status: %w", err)
	}
	alert := fmt.Sprintf("ArgoCD reports application %q (namespace %q) as sync=%s, health=%s.",
		cfg.App, cfg.Namespace, status.Sync, status.Health)
	if status.Message != "" {
		alert += " Health message: " + status.Message
	}
	alert += " Investigate, and fix it if you can do so safely."
	fmt.Fprintf(out, "ALERT: %s\n", alert)
	if cfg.DryRun {
		fmt.Fprintf(out, "\033[33mDRY RUN: actions will be validated but not applied.\033[0m\n")
	}
	audit.Write("start", map[string]any{
		"alert": alert, "model": cfg.Model, "effort": cfg.Effort,
		"dry_run": cfg.DryRun, "allowed_actions": cfg.AllowedActions, "max_actions": cfg.MaxActions,
	})

	tools := readOnlyTools(cfg)
	byName := map[string]Tool{}
	var toolParams []anthropic.BetaToolUnionParam
	var toolNames, actionNames []string
	for _, t := range tools {
		byName[t.Name] = t
		toolNames = append(toolNames, t.Name)
		props := t.Properties
		if props == nil {
			props = map[string]any{}
		}
		toolParams = append(toolParams, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: props, Required: t.Required},
		}})
	}
	// Only allowlisted actions are offered to the model. The guardrails check
	// the allowlist again on every call, in case the model asks for one anyway.
	guard := NewGuardrails(cfg)
	actionsByName := map[string]Action{}
	for _, a := range actions(cfg) {
		actionsByName[a.Name] = a
		if guard.Check(a.Name) != nil {
			continue
		}
		actionNames = append(actionNames, a.Name)
		toolParams = append(toolParams, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name:        a.Name,
			Description: anthropic.String(a.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: a.Properties, Required: a.Required},
		}})
	}
	diag := diagnosisToolParam()
	toolParams = append(toolParams, anthropic.BetaToolUnionParam{OfTool: &diag})
	var action *ActionRecord // the incident's action, if any

	stage(out, "SETUP", "what Claude may request (Claude only asks; this code runs each request)")
	fmt.Fprintf(out, "   read-only tools: %s\n", strings.Join(toolNames, ", "))
	fmt.Fprintf(out, "   actions:         %s (at most %d per incident, checked by guardrails)\n",
		strings.Join(actionNames, ", "), cfg.MaxActions)
	fmt.Fprintf(out, "   to finish:       %s\n", submitDiagnosisTool)

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(cfg.Model),
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt}},
		Tools:     toolParams,
		// Summarized thinking lets us show the agent's reasoning in the terminal.
		Thinking: anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
			Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized,
		}},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(cfg.Effort)},
		// If a safety classifier declines a request, let the API retry it on a
		// fallback model instead of ending the run.
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: "default"},
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(alert)),
		},
	}

	var inTokens, outTokens int64
	defer func() {
		fmt.Fprintf(out, "\nTokens: %d in, %d out\n", inTokens, outTokens)
		audit.Write("end", map[string]any{"input_tokens": inTokens, "output_tokens": outTokens})
	}()

	for step := 1; step <= cfg.MaxSteps; step++ {
		// Claude keeps no state between calls: each step sends the whole
		// conversation so far, and the reply is appended to it.
		stage(out, fmt.Sprintf("STEP %d/%d", step, cfg.MaxSteps),
			"sending the conversation to Claude (%s)", plural(len(params.Messages), "message"))
		start := time.Now()
		resp, err := client.Beta.Messages.New(ctx, params)
		if err != nil {
			audit.Write("error", map[string]any{"step": step, "error": err.Error()})
			return nil, fmt.Errorf("step %d: %w", step, err)
		}
		inTokens += resp.Usage.InputTokens
		outTokens += resp.Usage.OutputTokens
		fmt.Fprintf(out, "   Claude replied in %.1fs (stop_reason: %s; %d tokens in, %d out)\n",
			time.Since(start).Seconds(), resp.StopReason, resp.Usage.InputTokens, resp.Usage.OutputTokens)
		audit.Write("llm_response", map[string]any{
			"step": step, "stop_reason": resp.StopReason, "content": json.RawMessage(resp.RawJSON()),
		})

		if resp.StopReason == anthropic.BetaStopReasonRefusal {
			o := decideOutcome(nil, action)
			if o.Status != StatusResolved {
				o.Reason = "model declined the request"
			}
			return finish(audit, out, o), nil
		}

		// Keep the full assistant turn (including thinking) in the history.
		params.Messages = append(params.Messages, resp.ToParam())

		var results []anthropic.BetaContentBlockParamUnion
		var requested []string // what Claude asked for this step, for the progress line
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.BetaThinkingBlock:
				if t := strings.TrimSpace(b.Thinking); t != "" {
					fmt.Fprintf(out, "\033[2m   [thinking] %s\033[0m\n", t)
				}
			case anthropic.BetaTextBlock:
				fmt.Fprintf(out, "   Claude: %s\n", b.Text)
			case anthropic.BetaToolUseBlock:
				input := json.RawMessage(b.JSON.Input.Raw())
				if b.Name == submitDiagnosisTool {
					var d Diagnosis
					if err := json.Unmarshal(input, &d); err != nil {
						return nil, fmt.Errorf("parse diagnosis: %w", err)
					}
					audit.Write("diagnosis", map[string]any{"step": step, "diagnosis": d})
					stage(out, "REPORT", "Claude called %s: category %s, suspect commit %.7s, confidence %s, needs human %t",
						submitDiagnosisTool, d.Category, d.SuspectCommit, d.Confidence, d.NeedsHuman)
					cfg.progress("step %d (%.0fs): %s → %s, suspect %.7s", step, time.Since(start).Seconds(),
						submitDiagnosisTool, d.Category, d.SuspectCommit)
					return finish(audit, out, decideOutcome(&d, action)), nil
				}
				if a, ok := actionsByName[b.Name]; ok {
					var result anthropic.BetaContentBlockParamUnion
					var note string
					result, action, note = runAction(ctx, cfg, guard, a, action, b.ID, input, step, audit, out)
					results = append(results, result)
					requested = append(requested, a.Name+" → "+note)
					continue
				}
				results = append(results, runTool(ctx, byName, b.ID, b.Name, input, step, audit, out))
				requested = append(requested, b.Name)
			}
		}

		if resp.StopReason == anthropic.BetaStopReasonToolUse {
			// All results go back in a single user message.
			fmt.Fprintf(out, "   %s go back to Claude in the next step\n", plural(len(results), "tool result"))
			cfg.progress("step %d (%.0fs): %s", step, time.Since(start).Seconds(), strings.Join(requested, ", "))
			params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(results...))
			continue
		}
		// The model stopped without submitting a diagnosis: remind it once per step.
		fmt.Fprintf(out, "   Claude stopped without calling %s; reminding it\n", submitDiagnosisTool)
		cfg.progress("step %d (%.0fs): no tool call, reminding Claude to submit a diagnosis", step, time.Since(start).Seconds())
		params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(
			anthropic.NewBetaTextBlock("Call submit_diagnosis with your conclusion.")))
	}
	o := decideOutcome(nil, action)
	if o.Status != StatusResolved {
		o.Reason = fmt.Sprintf("hit the %d-step limit without a diagnosis", cfg.MaxSteps)
	}
	return finish(audit, out, o), nil
}

func finish(audit *AuditLog, out io.Writer, o Outcome) *Outcome {
	stage(out, "OUTCOME", "decided by the code from what happened, not by Claude: %s (%s)",
		strings.ToUpper(o.Status), o.Reason)
	audit.Write("outcome", map[string]any{"outcome": o})
	return &o
}

// stage prints a header for one phase of the loop, so the terminal shows where
// the agent is: observe, set up, each step, an action, the report, the outcome.
func stage(out io.Writer, name, detail string, args ...any) {
	fmt.Fprintf(out, "\n\033[1m── %s\033[0m  %s\n", name, fmt.Sprintf(detail, args...))
}

// runAction passes a mutating action through the guardrails, then plans,
// applies, and verifies it. It returns the tool_result for the model, the
// incident's (possibly updated) action record, and a short note on what
// happened for the progress line.
func runAction(ctx context.Context, cfg Config, guard *Guardrails, a Action, prev *ActionRecord,
	id string, input json.RawMessage, step int, audit *AuditLog, out io.Writer) (anthropic.BetaContentBlockParamUnion, *ActionRecord, string) {
	stage(out, "ACT", "Claude requested \033[35m%s %s\033[0m; the code checks it before anything changes", a.Name, compact(input))
	reply := func(msg string, isError bool) anthropic.BetaContentBlockParamUnion {
		return anthropic.NewBetaToolResultBlock(id, msg, isError)
	}

	// 1. Guardrails: allowlist and blast-radius limit.
	if err := guard.Check(a.Name); err != nil {
		fmt.Fprintf(out, "\033[33m   1. guardrails: 🛡 denied: %s\033[0m\n", err)
		audit.Write("guardrail_denied", map[string]any{"step": step, "action": a.Name, "input": input, "reason": err.Error()})
		return reply("Denied by guardrails: "+err.Error()+". Do not retry; call submit_diagnosis.", true), prev, "denied"
	}
	fmt.Fprintf(out, "   1. guardrails: passed (allowlisted, within the %d-action limit)\n", cfg.MaxActions)

	// 2. Plan: action-specific validation. Nothing has changed yet, so a
	// rejected plan doesn't count toward the incident's action limit.
	plan, err := a.Plan(ctx, input)
	if err != nil {
		fmt.Fprintf(out, "\033[33m   2. validate:   🛡 rejected: %s\033[0m\n", err)
		audit.Write("action_rejected", map[string]any{"step": step, "action": a.Name, "input": input, "reason": err.Error()})
		return reply("Action rejected by validation: "+err.Error(), true), prev, "rejected"
	}
	guard.RecordAttempt()
	rec := &ActionRecord{Name: a.Name, Summary: plan.Summary, Target: plan.Target, DryRun: cfg.DryRun}
	fmt.Fprintf(out, "\033[32m   2. validate:   🛡 allowed: %s\033[0m\n", plan.Summary)
	audit.Write("guardrail_allowed", map[string]any{"step": step, "action": a.Name, "input": input, "plan": plan.Summary})

	// 3. Apply, unless this is a dry run.
	if cfg.DryRun {
		fmt.Fprintf(out, "\033[33m   3. apply:      skipped (DRY RUN)\033[0m\n")
		audit.Write("action_dry_run", map[string]any{"step": step, "action": rec})
		return reply("DRY RUN: the action passed all checks but was not applied: "+plan.Summary+
			". No further actions are available. Call submit_diagnosis, answering needs_human as if the action had been applied.", false), rec, "validated, not applied (dry run)"
	}

	rev, err := plan.Apply(ctx)
	rec.Applied, rec.Revision = rev != "", rev
	if !rec.Applied {
		rec.Error = err.Error()
		fmt.Fprintf(out, "\033[31m   3. apply:      failed: %s\033[0m\n", err)
		audit.Write("action_failed", map[string]any{"step": step, "action": rec})
		return reply("Action failed: "+err.Error()+". No further actions are available. Call submit_diagnosis with needs_human=true.", true), rec, "apply failed"
	}
	fmt.Fprintf(out, "   3. apply:      %s (target revision %.7s)\n", plan.Applied, rev)
	audit.Write("action_applied", map[string]any{"step": step, "action": rec})

	// 4. Verify from the cluster's point of view, not the model's.
	fmt.Fprintf(out, "   4. verify:     waiting up to %s for ArgoCD to report Synced/Healthy at %.7s...\n", cfg.VerifyTimeout, rev)
	start := time.Now()
	st, err := cfg.WaitForRecovery(ctx, rev, cfg.VerifyTimeout)
	rec.Verified = err == nil
	audit.Write("action_verified", map[string]any{"step": step, "verified": rec.Verified, "status": st})
	if err != nil {
		rec.Error = err.Error()
		fmt.Fprintf(out, "\033[31m      ✗ not recovered: %s\033[0m\n", err)
		msg := "Applied " + plan.Summary + ", but " + err.Error() +
			". No further actions are available. Call submit_diagnosis with needs_human=true."
		return reply(msg, false), rec, fmt.Sprintf("applied %.7s, NOT recovered", rev)
	}
	fmt.Fprintf(out, "\033[32m      ✓ recovered after %.0fs: Synced/Healthy at %.7s\033[0m\n", time.Since(start).Seconds(), rev)
	msg := "Applied " + plan.Summary + ". Verified: the app is Synced and Healthy at " + rev +
		". Call submit_diagnosis to report what you found and did."
	return reply(msg, false), rec, fmt.Sprintf("applied %.7s, recovered", rev)
}

// runTool executes one tool call, logs it, and returns the tool_result block.
// Tool errors are returned to the model (is_error=true) so it can adjust.
func runTool(ctx context.Context, byName map[string]Tool, id, name string, input json.RawMessage,
	step int, audit *AuditLog, out io.Writer) anthropic.BetaContentBlockParamUnion {
	fmt.Fprintf(out, "\033[36m   → %s\033[0m\n", strings.TrimSpace(name+" "+compact(input)))

	tool, ok := byName[name]
	var output string
	var err error
	if !ok {
		err = fmt.Errorf("unknown tool %q", name)
	} else {
		output, err = tool.Run(ctx, input)
	}
	isError := err != nil
	if isError {
		output = err.Error()
		fmt.Fprintf(out, "\033[31m     error: %s\033[0m\n", firstLine(output))
	}
	audit.Write("tool_call", map[string]any{
		"step": step, "tool": name, "input": input, "output": output, "is_error": isError,
	})

	if len(output) > maxToolOutput {
		output = output[:maxToolOutput] + "\n[output truncated]"
	}
	return anthropic.NewBetaToolResultBlock(id, output, isError)
}

func compact(input json.RawMessage) string {
	if len(input) == 0 || string(input) == "{}" {
		return ""
	}
	return string(input)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool results longer than this are cut before being sent to the model (the
// audit log keeps the full output). Events and describe output are usually a
// few KB; this only guards against something unexpectedly huge.
const maxToolOutput = 20000

// ErrNoDiagnosis means the run ended without a submit_diagnosis call, so the
// incident should go to a human.
var ErrNoDiagnosis = errors.New("agent did not reach a diagnosis")

// Run investigates the app and returns the agent's diagnosis. This is the
// agent loop: send the conversation to Claude, run whatever tools it asks
// for, send the results back, and repeat until it calls submit_diagnosis.
func Run(ctx context.Context, cfg Config, client anthropic.Client, audit *AuditLog, out io.Writer) (*Diagnosis, error) {
	// Observe: start from what ArgoCD reports, the same signal an alert would carry.
	status, err := cfg.GetAppStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("read app status: %w", err)
	}
	alert := fmt.Sprintf("ArgoCD reports application %q (namespace %q) as sync=%s, health=%s.",
		cfg.App, cfg.Namespace, status.Sync, status.Health)
	if status.Message != "" {
		alert += " Health message: " + status.Message
	}
	alert += " Investigate and diagnose the problem."
	fmt.Fprintf(out, "ALERT: %s\n\n", alert)
	audit.Write("start", map[string]any{"alert": alert, "model": cfg.Model, "effort": cfg.Effort})

	tools := readOnlyTools(cfg)
	byName := map[string]Tool{}
	var toolParams []anthropic.BetaToolUnionParam
	for _, t := range tools {
		byName[t.Name] = t
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
	diag := diagnosisToolParam()
	toolParams = append(toolParams, anthropic.BetaToolUnionParam{OfTool: &diag})

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
		resp, err := client.Beta.Messages.New(ctx, params)
		if err != nil {
			audit.Write("error", map[string]any{"step": step, "error": err.Error()})
			return nil, fmt.Errorf("step %d: %w", step, err)
		}
		inTokens += resp.Usage.InputTokens
		outTokens += resp.Usage.OutputTokens
		audit.Write("llm_response", map[string]any{
			"step": step, "stop_reason": resp.StopReason, "content": json.RawMessage(resp.RawJSON()),
		})

		if resp.StopReason == anthropic.BetaStopReasonRefusal {
			return nil, fmt.Errorf("%w: model declined the request", ErrNoDiagnosis)
		}

		// Keep the full assistant turn (including thinking) in the history.
		params.Messages = append(params.Messages, resp.ToParam())

		var results []anthropic.BetaContentBlockParamUnion
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.BetaThinkingBlock:
				if b.Thinking != "" {
					fmt.Fprintf(out, "\033[2m[thinking] %s\033[0m\n", b.Thinking)
				}
			case anthropic.BetaTextBlock:
				fmt.Fprintf(out, "%s\n", b.Text)
			case anthropic.BetaToolUseBlock:
				input := json.RawMessage(b.JSON.Input.Raw())
				if b.Name == submitDiagnosisTool {
					var d Diagnosis
					if err := json.Unmarshal(input, &d); err != nil {
						return nil, fmt.Errorf("parse diagnosis: %w", err)
					}
					audit.Write("diagnosis", map[string]any{"step": step, "diagnosis": d})
					return &d, nil
				}
				results = append(results, runTool(ctx, byName, b.ID, b.Name, input, step, audit, out))
			}
		}

		if resp.StopReason == anthropic.BetaStopReasonToolUse {
			// All results go back in a single user message.
			params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(results...))
			continue
		}
		// The model stopped without submitting a diagnosis: remind it once per step.
		params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(
			anthropic.NewBetaTextBlock("Call submit_diagnosis with your conclusion.")))
	}
	return nil, fmt.Errorf("%w: hit the %d-step limit", ErrNoDiagnosis, cfg.MaxSteps)
}

// runTool executes one tool call, logs it, and returns the tool_result block.
// Tool errors are returned to the model (is_error=true) so it can adjust.
func runTool(ctx context.Context, byName map[string]Tool, id, name string, input json.RawMessage,
	step int, audit *AuditLog, out io.Writer) anthropic.BetaContentBlockParamUnion {
	fmt.Fprintf(out, "\033[36m→ %s %s\033[0m\n", name, compact(input))

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
		fmt.Fprintf(out, "\033[31m  error: %s\033[0m\n", firstLine(output))
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

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

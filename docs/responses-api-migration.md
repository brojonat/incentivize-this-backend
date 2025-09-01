# Responses API Migration

This document describes the desired API design and implementation approach for migrating from Chat Completions to the OpenAI Responses API. The goal is to simplify our agent loop, reduce payload size, and make tool-calling/state management first-class.

## Why Responses API

- Built-in state (memory): Maintain conversation context with `PreviousResponseID`; no need to resend history each turn.
- First-class tool use: Native function tools and built-in tools (file_search, web_search_preview, computer_use) with selective invocation.
- Simpler agent loops: Single endpoint for multi-step turns; send tool outputs back and continue with the same `response_id`.
- Structured output: Enforce JSON Schema for predictable, strongly-typed results.
- Multi-modal input: Add PDFs and images via input items alongside text.
- Built-in retrieval: `file_search` integrates with vector stores.

Reference: Getting Started with the OpenAI Responses API Using Go

## Design goals

- Collapse our provider surface to a minimal, opinionated API aligned with agent use cases.
- Delegate conversation memory to OpenAI via `response_id` instead of replaying history.
- Define tools at conversation start; subsequent turns send only function outputs.

## Target provider surface

We will support both a simple one-shot call and a multi-turn agent call.

```golang
type LLMProvider interface {
    // One-shot: instructions + message -> assistant text (no tool calls)
    GenerateResponse(ctx context.Context, instructions string, message string) (string, error)

    // Multi-turn (agent): one turn using Responses API
    //  - previousResponseID empty => start turn with message and tools
    //  - previousResponseID set   => continue turn with function outputs only
    GenerateResponsesTurn(
        ctx context.Context,
        previousResponseID string,
        userInput string,
        tools []Tool,
        functionOutputs map[string]string,
    ) (assistantText string, toolCalls []ToolCall, responseID string, err error)
}
```

### Tool and tool-call types

```golang
type Tool struct {
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    Parameters  map[string]interface{} `json:"parameters"` // JSON Schema
}

type ToolCall struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON string
}
```

## Multi-turn flow (Responses API)

1. First turn

```golang
params := responses.ResponseNewParams{
    Model:           openai.ChatModelGPT4o,
    Temperature:     openai.Float(0.7),
    MaxOutputTokens: openai.Int(10240),
    Tools:           availableTools,
    Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(prompt)},
    Store:           openai.Bool(true),
}
resp, _ := client.Responses.New(ctx, params)
text := resp.OutputText()
id := resp.ID
```

2. If the model requested tool calls, execute them and continue

```golang
turnInputs := []responses.ResponseInputItemUnionParam{}
for _, out := range resp.Output {
    if out.Type == "function_call" {
        fc := out.AsFunctionCall()
        result := runTool(ctx, fc.Name, fc.Arguments) // returns string JSON
        turnInputs = append(turnInputs, responses.ResponseInputItemParamOfFunctionCallOutput(fc.CallID, result))
    }
}
if len(turnInputs) == 0 {
    return text
}

params.PreviousResponseID = openai.String(id)
params.Tools = nil
params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: turnInputs}
resp, _ = client.Responses.New(ctx, params)
finalText := resp.OutputText()
```

## Structured output (optional)

When guaranteed JSON is required, configure `Text.Format` with a JSON Schema.

```golang
params.Text = responses.ResponseTextConfigParam{
    Format: responses.ResponseFormatTextConfigUnionParam{
        OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
            Name:        "HardenBounty",
            Schema:      hardenSchema, // map[string]any
            Strict:      openai.Bool(true),
            Description: openai.String("Hardened bounty requirements schema"),
            Type:        "json_schema",
        },
    },
}
```

## Orchestrator Loop

This is how the orchestrator loop should work at a high level (Python-style pseudocode):

```python
def run_orchestrator(provider, tools, prompt, max_turns=20):
    previous_response_id = None
    pending_outputs = {}

    for turn_idx in range(max_turns):
        # 1) Start vs continue
        if previous_response_id is None:
            assistant_text, tool_calls, response_id = provider.GenerateResponsesTurn(
                previous_response_id=None,
                userInput=prompt,
                tools=tools,                # Only first turn
                functionOutputs=None,
            )
        else:
            assistant_text, tool_calls, response_id = provider.GenerateResponsesTurn(
                previous_response_id=previous_response_id,
                userInput=None,
                tools=None,                 # Omit after first turn
                functionOutputs=pending_outputs,
            )

        previous_response_id = response_id
        pending_outputs = {}               # Clear; we’ll rebuild per turn

        # 2) Handle tool calls
        for call in tool_calls:
            if call.name == "submit_decision":
                decision = parse_json(call.arguments)  # { is_approved, reason, ... }
                return decision

            try:
                result = run_tool(call.name, call.arguments)  # stringified JSON
            except Exception as e:
                result = json.dumps({"error": f"{type(e).__name__}: {str(e)}"})

            # Key by the exact call_id the LLM gave us
            pending_outputs[call.id] = result

        # 3) Progress guards
        if not tool_calls and not assistant_text.strip():
            # No tool calls and no content → nothing to do, bail
            break

        # If we received assistant text only (no tool calls), let the loop continue
        # The model may need another turn to reach submit_decision.

    # If we reach here, max turns hit or no progress
    return {"is_approved": False, "reason": "Max turns reached or no actionable response"}
```

## Migration plan

1. Introduce Responses API client and a new `GenerateResponsesTurn` in the OpenAI provider.
2. Update the orchestrator to loop on `response_id` and feed tool outputs back.
3. Keep `GenerateResponse` for simple, one-shot use cases; under the hood it can use Responses without tools.
4. Migrate endpoints that currently rely on Chat Completions tool-calls to Responses progressively.

## Backwards compatibility

- Preserve current behavior where feasible; gate new behavior on provider capability when necessary.
- For non-OpenAI providers, keep the legacy path until stateful APIs exist.

## Pitfalls to avoid

- Don’t resend tool definitions on subsequent turns.
- Ensure BaseURL is correct for the SDK (commonly includes `/v1`).
- Use JSON Schema `strict` when you must parse reliably.

## Testing

- Unit test tool-call parsing and function output plumbing.
- Integration test: first turn → tool calls → second turn with outputs → final text.

# High-Level Plan: Adding Provider-Agnostic Tool Call Support

This document outlines a strategy for integrating tool-calling capabilities into the `LLMProvider` interface in a way that remains agnostic to the underlying provider (e.g., OpenAI, Anthropic, Gemini).

## 1. Overview of Tool Calling

Tool calling enables an LLM to perform actions and retrieve external information during a conversation. It's a multi-step process:

1.  **Request**: The application sends a prompt to the LLM, along with a list of available "tools" (functions) it can use.
2.  **Tool Invocation**: If the LLM determines that it needs to use a tool to answer the prompt, it responds not with a final message, but with a request to invoke one or more tools with specific arguments.
3.  **Execution**: The application receives this request, executes the specified tool(s) (e.g., calling an internal function or an external API), and obtains a result.
4.  **Response**: The application sends the tool's result back to the LLM.
5.  **Final Answer**: The LLM uses the tool's output to formulate and return a final, user-facing answer.

This flow allows an agent to, for example, fetch details about a Reddit post before deciding if a comment on that post meets a bounty's requirements.

## 2. Proposed Abstraction

To support this flow in a provider-agnostic way, we need to evolve our existing `LLMProvider` interface. The current `Complete(prompt string) -> string` method is synchronous and doesn't support the multi-turn nature of tool calling.

I propose the following set of data structures and a new interface method.

```go
// In abb/llm.go

// Tool defines a function the LLM can invoke.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	// Parameters defined as a JSON Schema object.
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolCall represents the LLM's request to call a specific tool.
type ToolCall struct {
	ID        string `json:"id"`        // A unique identifier for the tool call instance.
	Name      string `json:"name"`      // The name of the tool to be called.
	Arguments string `json:"arguments"` // A JSON string containing the arguments.
}

// Message represents a single message in the conversation history.
type Message struct {
	Role       string     `json:"role"`        // "user", "assistant", or "tool"
	Content    string     `json:"content"`     // Text content of the message.
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"` // For assistant messages requesting tool calls.
	ToolCallID string     `json:"tool_call_id,omitempty"` // For "tool" role messages, linking to a ToolCall.
}

// LLMResponse is the object returned by the LLM provider.
// It can either be a final answer or a request to call tools.
type LLMResponse struct {
	// The final text content, if available.
	Content string
	// A list of tool calls requested by the LLM. If this is not empty,
	// the application should execute the tools and send the results back.
	ToolCalls []ToolCall
}

// The updated LLMProvider interface.
type LLMProvider interface {
	// GenerateResponse sends a conversational history to the LLM and gets a response.
	GenerateResponse(ctx context.Context, messages []Message, tools []Tool) (*LLMResponse, error)
}
```

## 3. Implementation Plan

Here is a high-level, step-by-step plan to implement this abstraction.

### Step 1: Define Core Data Structures

Add the `Tool`, `ToolCall`, `Message`, and `LLMResponse` structs to `abb/llm.go`. These will serve as our common, provider-agnostic data models.

### Step 2: Update the `LLMProvider` Interface

In `abb/llm.go`, replace the existing `Complete` method in the `LLMProvider` interface with the new `GenerateResponse` method defined above.

### Step 3: Implement the New Interface for Each Provider

Update each concrete provider to implement the new `GenerateResponse` method.

- **`OpenAIProvider`**: Map our abstract `Message` and `Tool` structs to the corresponding structures in the OpenAI Chat Completions API. The API response will need to be parsed to check for `tool_calls` and populate our `LLMResponse` struct accordingly.
- **`AnthropicProvider`**: Do the same for Anthropic's Messages API. The logic will be very similar: map our structs to their format, and parse the response to see if the `stop_reason` is `tool_use`.
- **`GeminiProvider`**: If/when support for Gemini is added, a `GeminiProvider` would be created that follows the same pattern, mapping to its function calling API.

### Step 4: Define the Tool Suite

We must define a suite of available tools that the LLM agent can use to accomplish its goals. Instead of having monolithic activities that contain conditional logic, we should break down those activities into their core capabilities and expose them as tools.

Key activities to be refactored into tools include:

- **`PullContentActivity`**: Becomes a `get_content_details` tool. This is crucial for allowing the agent to perform "research" on parent posts, user profiles, or related content.
- **Image Analysis Logic**: Becomes an `analyze_image_for_visual_requirements` tool. The agent can call this when a bounty has specific visual criteria (e.g., "the screenshot must contain our logo").
- **`DetectMaliciousContent`**: Becomes a `scan_text_for_malicious_content` tool. This acts as a security guardrail that the agent can invoke.

Each of these tools will need a clear name, a detailed description of what it does and when to use it, and a well-defined JSON schema for its arguments.

### Step 5: Implement the Tool-Calling Control Loop as a Temporal Workflow

Instead of a simple function, the agent's main control loop should be implemented as a **Temporal Workflow**. This is the key insight from best practices, as it makes the entire multi-step conversation durable, stateful, and resilient to failure.

This "Orchestrator Workflow" will:

1.  Initialize the message history with the user's prompt.
2.  Enter a loop that calls an activity to wrap the provider's `GenerateResponse` method.
3.  Check the `LLMResponse` from the activity:
    - If `ToolCalls` is empty, the loop terminates and the workflow returns the `Content` as the final answer.
    - If `ToolCalls` is not empty, the workflow will execute the corresponding tools. Each tool execution should be wrapped in its own **activity**.
    - The workflow appends the tool results to the message history as `role: "tool"` messages.
4.  The loop continues, feeding the updated history back to the LLM in the next iteration.

This orchestrator workflow would be the ideal place to manage the agent's logic, likely living in `abb/workflow.go`.

### Step 6: Refactor High-Level Logic into an Agentic Workflow

This is the most significant architectural change. Instead of calling a specific, high-level activity like `CheckContentRequirementsActivity` directly, we will now invoke the new `OrchestratorWorkflow` with a high-level goal.

For example, the process of validating a user's submission would change as follows:

- **Before**: `ExecuteActivity("CheckContentRequirementsActivity", ...)`
- **After**: `ExecuteWorkflow("OrchestratorWorkflow", Goal: "Analyze this submitted Reddit comment against the bounty requirements and determine if it is valid for payout.")`

The `OrchestratorWorkflow` will be provided with the entire suite of tools defined in Step 4. Its internal LLM will then autonomously create and execute a plan. It might decide to:

1.  First, call `get_content_details` on the parent post to check if it's stickied.
2.  Then, call `scan_text_for_malicious_content` on the user's comment.
3.  Finally, synthesize this information to produce a final judgment on whether the requirements are met.

This refactoring moves our system from a hard-coded sequence of operations to a dynamic, intelligent, and more capable agent-based architecture. The agent loop itself determines which conditional activities need to be called.

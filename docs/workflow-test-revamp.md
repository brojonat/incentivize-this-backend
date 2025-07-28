# Workflow Test Documentation

This document provides detailed explanations of the workflow tests in `abb/workflow_test.go`, covering what each test does and how it implements the testing patterns.

## Table of Contents

1. [Test Suite Setup](#test-suite-setup)
2. [OrchestratorWorkflow Tests](#orchestratorworkflow-tests)
3. [BountyAssessmentWorkflow Tests](#bountyassessmentworkflow-tests)
4. [Simple Workflow Tests](#simple-workflow-tests)
5. [Testing Patterns](#testing-patterns)

## Test Suite Setup

### WorkflowTestSuite Structure

```go
type WorkflowTestSuite struct {
    suite.Suite
    testsuite.WorkflowTestSuite
    env *testsuite.TestWorkflowEnvironment
}
```

The test suite uses Temporal's testing framework with the following setup:

- **SetupTest()**: Initializes a new test environment for each test
- **AfterTest()**: Validates all mocked expectations were met
- **Workflow Registration**: Only registers the workflows needed globally (not OrchestratorWorkflow which is mocked)
- **Activity Registration**: Registers all activities for mocking

### Key Testing Components

- **Test Environment**: `TestWorkflowEnvironment` provides isolated workflow execution
- **Activity Mocking**: Uses `OnActivity()` to mock activity responses
- **Signal Testing**: Uses `RegisterDelayedCallback()` for proper signal timing
- **Child Workflow Mocking**: Uses `RegisterWorkflowWithOptions()` to mock child workflows

## OrchestratorWorkflow Tests

The OrchestratorWorkflow is the core LLM orchestration engine that handles tool calls and manages conversations.

### Test_OrchestratorWorkflow_NoToolCalls_Success

**Purpose**: Tests the simplest case where the LLM provides a direct response without needing any tools.

**How it works**:

1. **Setup**: Creates a simple goal ("What is 2+2?") with no tools available
2. **Mocking**: Mocks a single `GenerateResponse` call that returns a direct answer
3. **Execution**: Runs the workflow and expects immediate completion
4. **Validation**: Verifies the workflow completes successfully with the expected response

**Key Pattern**: Direct LLM response without tool orchestration.

```go
input := OrchestratorWorkflowInput{
    Goal:  "What is 2+2?",
    Tools: []Tool{},
}

s.env.OnActivity("GenerateResponse", mock.Anything,
    []Message{{Role: "user", Content: "What is 2+2?"}},
    []Tool{}).
    Return(&LLMResponse{Content: "The answer is 4."}, nil)
```

### Test_OrchestratorWorkflow_ToolCall_Success

**Purpose**: Tests the full tool orchestration flow where the LLM makes a tool call and then provides a final response.

**How it works**:

1. **First LLM Call**: Mocks LLM requesting the `get_content_details` tool
2. **Tool Execution**: Mocks the `PullContentActivity` returning sample content
3. **Second LLM Call**: Mocks LLM processing tool results and providing final response
4. **Message History**: Validates proper message chain with user → assistant → tool → assistant flow

**Key Pattern**: Multi-turn conversation with tool execution.

```go
// First call - LLM requests tool
s.env.OnActivity("GenerateResponse", ...).Return(&LLMResponse{
    ToolCalls: []ToolCall{{
        ID: "call1", Name: "get_content_details",
        Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`,
    }},
}, nil)

// Tool execution
s.env.OnActivity("PullContentActivity", ...).Return([]byte(`{"title":"Test Post"}`), nil)

// Final LLM call with complete message history
expectedMessages := []Message{
    {Role: "user", Content: "Get content details for post 123"},
    {Role: "assistant", ToolCalls: [...]},
    {Role: "tool", ToolCallID: "call1", Content: `{"title":"Test Post"}`},
}
```

### Test_OrchestratorWorkflow_ToolCall_Failure

**Purpose**: Tests error handling when tool execution fails.

**How it works**:

1. **Setup**: LLM requests a tool that will fail
2. **Tool Failure**: Mocks `PullContentActivity` returning an error
3. **Error Handling**: LLM receives error information and provides appropriate response
4. **Validation**: Workflow completes successfully despite tool failure

**Key Pattern**: Graceful error handling in tool execution.

### Test_OrchestratorWorkflow_UnknownTool

**Purpose**: Tests handling of requests for tools that don't exist.

**How it works**:

1. **Invalid Request**: LLM requests a tool named "unknown_tool"
2. **Error Response**: System returns error message about unknown tool
3. **Recovery**: LLM processes the error and provides appropriate response

**Key Pattern**: Robust handling of invalid tool requests.

### Test_OrchestratorWorkflow_MaxTurnsExceeded

**Purpose**: Tests the infinite loop protection mechanism.

**How it works**:

1. **Infinite Loop Setup**: Mocks LLM to continuously request tools
2. **Turn Limit**: Workflow should stop after 10 turns to prevent infinite execution
3. **Error Validation**: Expects specific `MaxTurnsExceeded` error type

**Key Pattern**: Protection against runaway workflow execution.

## BountyAssessmentWorkflow Tests

The BountyAssessmentWorkflow manages bounty funding, listens for claim signals, and processes payouts.

### Test_BountyAssessmentWorkflow_FundingFailed

**Purpose**: Tests workflow behavior when bounty funding check fails.

**How it works**:

1. **Setup**: Creates a bounty assessment workflow input
2. **Funding Failure**: Mocks `CheckBountyFundedActivity` to return `false`
3. **Early Exit**: Workflow should complete immediately without listening for signals
4. **Validation**: No errors should occur, workflow just exits gracefully

**Key Pattern**: Early termination on funding failure.

```go
s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, "default-test-workflow-id").
    Return(false, nil)
```

### Test_BountyAssessmentWorkflow_SuccessfulClaim

**Purpose**: Tests the complete successful bounty claim flow including payout.

**How it works**:

1. **Setup**: Creates bounty with funding equal to one payout (will drain immediately)
2. **Funding Success**: Mocks successful funding check
3. **Child Workflow Mock**: Registers mock OrchestratorWorkflow that approves the claim
4. **Payout Activity**: Mocks successful payout activity
5. **Signal Timing**: Uses `RegisterDelayedCallback` to send claim signal during execution
6. **Validation**: Workflow completes when bounty is drained

**Key Pattern**: Complete end-to-end bounty claim with signal-based triggering.

```go
// Mock child workflow to approve claim
s.env.RegisterWorkflowWithOptions(func(ctx workflow.Context, input OrchestratorWorkflowInput) (*OrchestratorWorkflowOutput, error) {
    return &OrchestratorWorkflowOutput{
        IsApproved: true,
        Reason:     "Content meets requirements",
        Payout:     bountyPerPost,
    }, nil
}, workflow.RegisterOptions{Name: "OrchestratorWorkflow"})

// Send signal at right time
s.env.RegisterDelayedCallback(func() {
    s.env.SignalWorkflow(AssessmentSignalName, AssessContentSignal{...})
}, 0)
```

### Test_BountyAssessmentWorkflow_RejectedClaim

**Purpose**: Tests workflow behavior when a claim is rejected by the orchestrator.

**How it works**:

1. **Setup**: Creates bounty with longer timeout since it won't drain
2. **Rejection Mock**: Registers mock OrchestratorWorkflow that rejects the claim
3. **Signal Processing**: Sends claim signal which gets processed and rejected
4. **Timeout**: Workflow continues until timeout since bounty isn't drained

**Key Pattern**: Signal processing with rejection handling.

### Test_BountyAssessmentWorkflow_Timeout

**Purpose**: Tests workflow timeout behavior when no claims are submitted.

**How it works**:

1. **Setup**: Very short timeout (1ms) with no signals sent
2. **Timeout**: Workflow should complete due to timeout timer
3. **Validation**: Clean completion without errors

**Key Pattern**: Timeout-based workflow termination.

### Test_BountyAssessmentWorkflow_ClaimCooldown

**Purpose**: Tests the cooldown mechanism preventing duplicate claims for the same content.

**How it works**:

1. **Setup**: Sends two identical signals for the same content ID
2. **First Processing**: First signal gets processed and rejected
3. **Cooldown**: Second signal is ignored due to cooldown period
4. **Validation**: Child workflow is only called once, proving cooldown works

**Key Pattern**: Duplicate prevention with time-based cooldown.

```go
// Track child workflow calls
callCount := 0
s.env.RegisterWorkflowWithOptions(func(...) {
    callCount++  // This should only happen once
    return &OrchestratorWorkflowOutput{IsApproved: false, ...}
}, ...)

// Send duplicate signals
s.env.RegisterDelayedCallback(func() {
    s.env.SignalWorkflow(AssessmentSignalName, signal)
    s.env.SignalWorkflow(AssessmentSignalName, signal) // Should be ignored
}, 0)

// Verify only called once
s.Equal(1, callCount)
```

## Simple Workflow Tests

### ContactUsNotifyWorkflow Tests

These test the contact form notification system.

#### Test_ContactUsNotifyWorkflow_Success

- **Purpose**: Tests successful email sending
- **Pattern**: Simple activity mocking with successful return

#### Test_ContactUsNotifyWorkflow_EmailFailure

- **Purpose**: Tests error handling when email service fails
- **Pattern**: Activity failure with error propagation validation

### EmailTokenWorkflow Tests

These test the token email delivery system for Gumroad sales.

#### Test_EmailTokenWorkflow_Success

- **Purpose**: Tests complete successful flow of sending token email and marking sale as notified
- **Pattern**: Sequential activity execution with both activities succeeding

#### Test_EmailTokenWorkflow_EmailFailure

- **Purpose**: Tests workflow failure when token email cannot be sent
- **Pattern**: First activity fails, workflow terminates with error

#### Test_EmailTokenWorkflow_MarkNotifiedFailure

- **Purpose**: Tests workflow failure when database update fails after successful email
- **Pattern**: First activity succeeds, second activity fails

## Testing Patterns

### 1. Activity Mocking Pattern

```go
s.env.OnActivity("ActivityName", mock.Anything, expectedInput).
    Return(expectedOutput, expectedError)
```

**Key Points**:

- Use `mock.Anything` for context parameter
- Specify exact input parameters for validation
- Use `.Once()` to limit call count when needed

### 2. Signal-Based Workflow Testing

```go
s.env.RegisterDelayedCallback(func() {
    s.env.SignalWorkflow(SignalName, signalData)
}, 0)

s.env.ExecuteWorkflow(WorkflowFunc, input)
```

**Key Points**:

- Use `RegisterDelayedCallback` to send signals during execution
- Delay of `0` sends signal immediately when workflow starts listening
- Send signals before `ExecuteWorkflow` call

### 3. Child Workflow Mocking

```go
s.env.RegisterWorkflowWithOptions(mockWorkflowFunc,
    workflow.RegisterOptions{Name: "ChildWorkflowName"})
```

**Key Points**:

- Register mock before executing parent workflow
- Use exact workflow name that parent expects
- Mock function should match expected signature

### 4. Error Testing Pattern

```go
s.env.ExecuteWorkflow(WorkflowFunc, input)

s.True(s.env.IsWorkflowCompleted())
err := s.env.GetWorkflowError()
s.Error(err)
s.Contains(err.Error(), "expected error message")
```

**Key Points**:

- Always check workflow completion status
- Validate specific error types when needed
- Check error message content for specificity

### 5. State Validation Pattern

```go
var result *WorkflowOutput
s.NoError(s.env.GetWorkflowResult(&result))
s.True(result.IsApproved)
s.Equal("expected value", result.SomeField)
```

**Key Points**:

- Extract workflow results for validation
- Check both success conditions and specific output values
- Use appropriate assertion methods for different data types

## Best Practices

1. **Isolation**: Each test should be completely independent
2. **Mocking**: Mock all external dependencies (activities, child workflows)
3. **Timing**: Use `RegisterDelayedCallback` for signal-based workflows
4. **Validation**: Test both success and failure scenarios
5. **Clarity**: Test names should clearly indicate what scenario is being tested
6. **Coverage**: Test edge cases like timeouts, errors, and boundary conditions

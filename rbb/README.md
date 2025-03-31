# rbb

This package implements the Temporal workflows and activities that power the Reddit Bounty Board application.

## Core Components

### Workflows

The package includes several workflow implementations:

- **BountyAssessmentWorkflow**: Manages the assessment of Reddit content against bounty requirements and handles payments
- **PullRedditContentWorkflow**: Retrieves content from Reddit for verification
- **CheckContentRequirementsWorkflow**: Verifies if content satisfies specified requirements using LLM
- **PayBountyWorkflow**: Processes payments to users when content meets requirements
- **ReturnBountyToOwnerWorkflow**: Returns remaining bounty amounts to the original owner

### Activities

Activities are the building blocks that workflows orchestrate:

- **PullRedditContent**: Fetches content from Reddit's API
- **CheckContentRequirements**: Uses LLM to analyze content against requirements
- **TransferUSDC**: Handles USDC transfers on Solana blockchain
- **PayBounty**: Processes payment to users
- **ReturnBountyToOwner**: Returns funds to advertisers

## Testing

The workflow tests use Temporal's test framework to verify the correct behavior of workflows and activities. Tests include:

- Mocked activity implementations
- Signal handling verification
- Timeout handling
- Error handling

Run the tests with:

```bash
make test-rbb
```

For workflow-specific tests:

```bash
make test-workflow
```

# worker

This package provides the implementation for running Temporal workers that process workflows and activities.

## Core Components

### Worker Configuration

The worker setup includes:

- Connection to Temporal server
- Registration of workflows and activities
- Environment variable handling for configuration
- Solana blockchain integration setup

### Activity Registration

The worker registers various activities:

- Reddit content retrieval
- LLM-based content verification
- Solana blockchain activities
- Payment and escrow management

## Testing

While this package primarily contains integration code, you can run the project's workflow tests with:

```bash
make test-workflow
```

These tests verify that the workers correctly register and execute the expected workflows and activities.

## Running Locally

To run the worker locally:

```bash
make run-worker-local
```

This will start a local Temporal worker that connects to your Temporal server.

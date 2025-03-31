# solana

This package provides the Solana blockchain integration for the Reddit Bounty Board application.

## Core Components

### USDC Amount Handling

The package includes utilities for working with USDC amounts:

- Converting between different USDC representations
- Handling USDC math operations
- Validating USDC amounts

### Solana Client

A wrapper around the Solana Go SDK that provides:

- USDC token operations
- Escrow account management
- Transaction handling

## Testing

The Solana package includes comprehensive tests for:

- USDC amount conversions and operations
- Mock implementations for testing without a real blockchain connection
- Validation functions

Run the tests with:

```bash
make test-solana
```

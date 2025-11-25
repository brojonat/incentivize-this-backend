# Multi-Endpoint RPC Strategy for Solana

## Problem Statement

Currently, Solana RPC access is configured with a single endpoint. This creates several issues:

1. **Rate Limiting**: Single provider rate limits can block operations
2. **Cost Scaling**: As usage grows, costs increase linearly (e.g., Helius paid tiers)
3. **Single Point of Failure**: If the provider has downtime, all operations fail
4. **Vendor Lock-in**: Switching providers requires code/config changes

## Proposed Solution: Random Endpoint Selection

Distribute RPC queries across multiple providers using random selection.

### Why Random Selection (vs Round-Robin)?

In a **stateless, distributed system** (like Temporal workflows), random selection is superior:

| Approach | Pros | Cons |
|----------|------|------|
| **Random** | ✅ Stateless<br>✅ Works across distributed workers<br>✅ Good distribution over many calls<br>✅ Simple implementation | ❌ Not perfectly uniform |
| **Round-Robin** | ✅ Perfectly uniform distribution | ❌ Requires shared state (Redis/DB)<br>❌ Complex in distributed systems<br>❌ Adds latency and failure points |

For workflow activities that execute across multiple workers, random selection achieves similar distribution without coordination overhead.

## Implementation Plan

### 1. Update Configuration Structure

**Current:**
```go
type SolanaConfig struct {
    RPCEndpoint string `json:"rpc_endpoint"` // Single endpoint
    // ...
}
```

**Proposed:**
```go
type SolanaConfig struct {
    RPCEndpoints []string `json:"rpc_endpoints"` // Multiple endpoints
    // ...
}
```

### 2. Environment Variable Format

**Current:**
```bash
SOLANA_RPC_ENDPOINT=https://mainnet.helius-rpc.com
```

**Proposed:**
```bash
SOLANA_RPC_ENDPOINTS=https://api.mainnet-beta.solana.com,https://mainnet.helius-rpc.com,https://solana-api.projectserum.com
```

Parse as comma-separated list, trim whitespace from each endpoint.

### 3. Create Endpoint Selector

Add utility function in `solana/solana.go` (or equivalent):

```go
// SelectRandomEndpoint picks a random endpoint from the pool.
// Returns error if endpoints slice is empty.
func SelectRandomEndpoint(endpoints []string) (string, error) {
    if len(endpoints) == 0 {
        return "", fmt.Errorf("no RPC endpoints configured")
    }
    return endpoints[rand.Intn(len(endpoints))], nil
}
```

**Note:** Ensure `rand` is seeded appropriately. In Go 1.20+, `rand.Intn()` is automatically seeded.

### 4. Update Configuration Loading

**In activity configuration (e.g., `getConfiguration()` equivalent):**

```go
// Parse endpoints from environment variable
rpcEndpointsStr := os.Getenv("SOLANA_RPC_ENDPOINTS")
if rpcEndpointsStr == "" {
    return nil, fmt.Errorf("SOLANA_RPC_ENDPOINTS not set")
}

// Split and trim
endpoints := strings.Split(rpcEndpointsStr, ",")
for i := range endpoints {
    endpoints[i] = strings.TrimSpace(endpoints[i])
}

solanaConfig.RPCEndpoints = endpoints
```

### 5. Update RPC Client Initialization

**In workflow activities:**

```go
// Get configuration with endpoint pool
cfg, err := getConfiguration(ctx)
if err != nil {
    return err
}

// Select random endpoint
endpoint, err := solanautil.SelectRandomEndpoint(cfg.SolanaConfig.RPCEndpoints)
if err != nil {
    return err
}

// Create RPC client with selected endpoint
rpcClient := solanautil.NewRPCClient(endpoint)
```

### 6. Update Environment Examples

Update `.env.example` files:

```bash
# Solana RPC Endpoints (comma-separated list)
# Use multiple endpoints to distribute load and avoid rate limits
# Mix of free public endpoints and paid services
SOLANA_RPC_ENDPOINTS=https://api.mainnet-beta.solana.com,https://mainnet.helius-rpc.com,https://solana-api.projectserum.com

# Other Solana config...
SOLANA_ESCROW_WALLET=your_escrow_wallet
SOLANA_ESCROW_PRIVATE_KEY=your_escrow_private_key
SOLANA_USDC_MINT_ADDRESS=your_usdc_mint
```

## Testing Strategy

### Unit Tests

Test the endpoint selector:

```go
func TestSelectRandomEndpoint(t *testing.T) {
    endpoints := []string{
        "https://endpoint1.com",
        "https://endpoint2.com",
        "https://endpoint3.com",
    }

    // Test successful selection
    selected, err := SelectRandomEndpoint(endpoints)
    require.NoError(t, err)
    assert.Contains(t, endpoints, selected)

    // Test empty slice
    _, err = SelectRandomEndpoint([]string{})
    assert.Error(t, err)
}
```

### Integration Tests

Verify configuration loading:

```go
func TestConfigurationLoading(t *testing.T) {
    os.Setenv("SOLANA_RPC_ENDPOINTS", "https://a.com, https://b.com, https://c.com")
    defer os.Unsetenv("SOLANA_RPC_ENDPOINTS")

    cfg, err := getConfiguration(context.Background())
    require.NoError(t, err)
    assert.Len(t, cfg.SolanaConfig.RPCEndpoints, 3)
    assert.Equal(t, "https://a.com", cfg.SolanaConfig.RPCEndpoints[0])
    assert.Equal(t, "https://b.com", cfg.SolanaConfig.RPCEndpoints[1])
    assert.Equal(t, "https://c.com", cfg.SolanaConfig.RPCEndpoints[2])
}
```

## Expected Benefits

1. **Rate Limit Mitigation**: Distribute queries across multiple providers
2. **Cost Optimization**: Mix free public endpoints with paid services
3. **Improved Reliability**: If one provider is slow/down, others handle load
4. **Flexibility**: Easy to add/remove providers by updating env var
5. **No Vendor Lock-in**: Can switch providers without code changes

## Recommended Endpoint Mix

For production, consider mixing:

- **Free Public RPCs**: For basic queries, low-priority operations
  - `https://api.mainnet-beta.solana.com` (Solana Foundation)
  - `https://solana-api.projectserum.com` (Project Serum)

- **Paid Services**: For critical operations, higher rate limits
  - Helius (your current provider)
  - Alchemy
  - QuickNode

## Future Enhancements (Not in Initial Implementation)

These can be added later if needed:

1. **Health Checking**: Periodically test endpoints, remove unhealthy ones from pool
2. **Weighted Selection**: Prefer certain endpoints (e.g., 70% paid, 30% free)
3. **Retry with Different Endpoint**: On failure, retry with different endpoint
4. **Metrics/Logging**: Track which endpoints are used, error rates per endpoint
5. **Circuit Breaker**: Temporarily remove failing endpoints from pool

## Migration Checklist

- [ ] Add `SelectRandomEndpoint()` function to `solana/solana.go` (or equivalent)
- [ ] Update `SolanaConfig` struct to use `RPCEndpoints []string`
- [ ] Update configuration loading to parse comma-separated endpoints
- [ ] Update all RPC client initialization to use random endpoint selection
- [ ] Update `.env.example` files with new variable format
- [ ] Add unit tests for endpoint selection
- [ ] Add integration tests for configuration loading
- [ ] Update documentation/README with new configuration format
- [ ] Deploy with multiple endpoints configured
- [ ] Monitor logs to verify endpoint distribution

## Notes

- **No WebSocket Changes**: This strategy applies only to HTTP RPC endpoints, not WebSocket connections
- **No Backward Compatibility**: Clean migration to `SOLANA_RPC_ENDPOINTS` (plural), no need to support old `SOLANA_RPC_ENDPOINT` (singular)
- **No Health Checking**: Initial implementation doesn't need health checks on startup
- **Go 1.20+ Random**: Modern Go versions automatically seed `rand`, no manual seeding needed

## References

- Solana RPC Endpoints: https://docs.solana.com/cluster/rpc-endpoints
- Helius: https://www.helius.dev/
- Alchemy: https://www.alchemy.com/solana
- QuickNode: https://www.quicknode.com/chains/sol

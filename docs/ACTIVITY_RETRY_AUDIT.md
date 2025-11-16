# Activity Retry Policy Audit

## Executive Summary

**Date**: 2025-10-26
**Status**: 🚨 Multiple critical issues found

### Critical Findings
- ✅ **USDC transfers**: NOW SECURED (non-retryable)
- 🚨 **Email activities**: RETRYABLE (could send duplicates)
- 🚨 **Social posting**: RETRYABLE (could create duplicate posts)
- 🚨 **Database writes**: RETRYABLE (potential data corruption)
- ⚠️ **LLM calls**: NO EXPLICIT TIMEOUTS (could hang workflows)

---

## Activity Categories

### 🔴 CRITICAL - Must NOT Retry (Side Effects)

#### 1. USDC Transfer Activities ✅ SECURED
- `TransferUSDC` - ✅ Non-retryable after send
- `PayBountyActivity` - ✅ Non-retryable
- `RefundBountyActivity` - ✅ Non-retryable
- **Status**: FIXED with enforcement tests

#### 2. Email Activities 🚨 VULNERABLE
**Risk**: Duplicate emails to users

- `SendTokenEmail` (activity_email.go:23)
  - Returns: `fmt.Errorf()` - **RETRYABLE**
  - Impact: Users receive multiple tokens
  - Fix: Config errors non-retryable, SMTP errors limited retries (2-3 max)

- `SendContactUsEmail` (activity_email.go:63)
  - Returns: `fmt.Errorf()` - **RETRYABLE**
  - Impact: Admin receives duplicate notifications
  - Fix: Same as above

- `SendBountySummaryEmail` (activity_email.go:111)
  - Returns: `fmt.Errorf()` - **RETRYABLE**
  - Impact: Admin receives duplicate summaries
  - Fix: Same as above

#### 3. Social Media Posting 🚨 VULNERABLE
**Risk**: Duplicate posts on social platforms

- `PublishNewBountyReddit` (activity_reddit.go:756)
  - Returns: `fmt.Errorf()` - **RETRYABLE**
  - Impact: Duplicate Reddit posts
  - Fix: After successful post, mark non-retryable

- `postToReddit` (activity_reddit.go:855)
  - Returns: `fmt.Errorf()` - **RETRYABLE**
  - Impact: Duplicate Reddit posts
  - Fix: Mark non-retryable after POST succeeds

- `PublishNewBountyDiscord` (activity_discord.go:18)
  - Returns: `fmt.Errorf()` - **RETRYABLE**
  - Impact: Duplicate Discord messages
  - Fix: After successful post, mark non-retryable

- `postToDiscord` (activity_discord.go:105)
  - Returns: plain errors - **RETRYABLE**
  - Impact: Duplicate Discord messages
  - Fix: Mark non-retryable after POST succeeds

#### 4. Database Write Activities 🚨 NEEDS AUDIT
**Risk**: Data corruption or duplicate records

- `MarkGumroadSaleNotifiedActivity` (activity.go)
  - Needs audit: Could mark same sale as notified multiple times

- `DeleteBountyEmbeddingViaHTTPActivity` (activity.go)
  - Needs audit: Idempotent but should verify

- `GenerateAndStoreBountyEmbeddingActivity` (activity_embedding.go)
  - Needs audit: Could create duplicate embeddings

- `SummarizeAndStoreBountyActivity` (activity_summary.go)
  - Needs audit: Could create duplicate summaries

- `PruneStaleEmbeddingsActivity` (activity.go)
  - Needs audit: Should be idempotent

---

### 🟡 IMPORTANT - Must Have Timeouts

#### 5. LLM API Calls ⚠️ NO EXPLICIT TIMEOUTS
**Risk**: Workflows hanging indefinitely

- `GenerateResponsesTurn` (activity.go)
  - Current: Uses HTTP client timeout (if set)
  - Recommendation: Explicit activity timeout + retry limit

- `AnalyzeImageURL` (activity.go)
  - Current: Uses HTTP client timeout (if set)
  - Recommendation: Explicit activity timeout + retry limit

- `DetectMaliciousContent` (activity.go)
  - Current: Uses HTTP client timeout (if set)
  - Recommendation: Explicit activity timeout + retry limit

#### 6. Embedding Generation ⚠️ NEEDS REVIEW
- `GenerateAndStoreBountyEmbeddingActivity`
  - Recommendation: Timeout + limit retries (embedding APIs can be slow)

- `SummarizeAndStoreBountyActivity`
  - Recommendation: Timeout + limit retries

---

### 🟢 LOWER RISK - Read-Only (Can Retry Safely)

#### 7. Platform Content Fetching ✅ SAFE TO RETRY
- All `Pull*ContentActivity` functions (Reddit, GitHub, Bluesky, etc.)
- All `Get*` functions (user stats, metadata, etc.)
- **Current**: Retryable (safe, as these are read-only)
- **Recommendation**: Add reasonable timeouts (30s-60s) to prevent hanging

---

## Recommended Fixes (Priority Order)

### Priority 1: IMMEDIATE (This Week)
1. ✅ **USDC transfers** - COMPLETED
2. 🚨 **Email activities** - Make non-retryable after send attempt
3. 🚨 **Social posting** - Make non-retryable after successful POST

### Priority 2: HIGH (Next Sprint)
4. **Database writes** - Audit and fix non-idempotent operations
5. **LLM calls** - Add explicit timeouts and retry limits

### Priority 3: MEDIUM (Future)
6. **Embedding generation** - Add timeouts
7. **Platform fetching** - Add timeouts

---

## Implementation Patterns

### Pattern 1: Non-Retryable After Action
```go
// After attempting side effect (email, post, etc.)
return temporal.NewApplicationErrorWithOptions(
    fmt.Sprintf("failed to send: %s", err.Error()),
    "SEND_FAILED",
    temporal.ApplicationErrorOptions{
        NonRetryable: true,
    },
)
```

### Pattern 2: Limited Retries
```go
// Use Temporal's retry policy (set at workflow level)
// Default: exponential backoff with max attempts
info := activity.GetInfo(ctx)
if info.Attempt > 3 {
    return temporal.NewApplicationErrorWithOptions(
        "max retries exceeded",
        "MAX_RETRIES",
        temporal.ApplicationErrorOptions{
            NonRetryable: true,
        },
    )
}
return fmt.Errorf("retryable error: %w", err)
```

### Pattern 3: Config Errors Always Non-Retryable
```go
if configValue == "" {
    return temporal.NewApplicationErrorWithOptions(
        "CONFIG_VALUE not set",
        "CONFIGURATION_ERROR",
        temporal.ApplicationErrorOptions{
            NonRetryable: true,
        },
    )
}
```

---

## Test Requirements

### All side-effect activities MUST have:
1. Test that verifies errors are non-retryable
2. Test that verifies no action is taken on retry (if activity is called again)
3. Timeout tests to ensure workflows don't hang

### Example Test Pattern:
```go
func TestEmailActivity_NonRetryable(t *testing.T) {
    // ... setup ...
    _, err := env.ExecuteActivity(activities.SendEmail, ...)

    if err != nil {
        var appErr *temporal.ApplicationError
        require.ErrorAs(t, err, &appErr)
        assert.True(t, appErr.NonRetryable(),
            "CRITICAL: Email errors must be non-retryable to prevent duplicates")
    }
}
```

---

## Summary Statistics

- **Total activities audited**: ~45
- **Critical vulnerabilities**: 8 (emails + posting)
- **Activities needing timeouts**: ~12 (LLM + embeddings)
- **Safe to retry**: ~25 (read-only content fetching)

---

## Next Steps

1. Create GitHub issue for each critical finding
2. Implement fixes for Priority 1 items
3. Add enforcement tests for all side-effect activities
4. Document retry policies in code comments

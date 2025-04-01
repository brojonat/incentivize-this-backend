# rbb

This package implements the Temporal workflows and activities that power the Reddit Bounty Board application.

## Core Components

### Workflows

The package includes several workflow implementations:

- **BountyAssessmentWorkflow**: Manages bounty assessment, payment, and refunds
- **PullContentWorkflow**: Retrieves content from various platforms for verification
- **CheckContentRequirementsWorkflow**: Evaluates if content satisfies requirements
- **PayBountyWorkflow**: Processes payment to users
- **ReturnBountyToOwnerWorkflow**: Returns funds to advertisers

### Activities

Activities are the building blocks that workflows orchestrate:

- **PullRedditContent**: Fetches content from Reddit's API (supports both posts and comments)
- **CheckContentRequirements**: Uses LLM to analyze content against requirements
- **TransferUSDC**: Handles USDC transfers on Solana blockchain
- **PayBounty**: Processes payment to users
- **ReturnBountyToOwner**: Returns funds to advertisers

## Platform Support

The system supports multiple content platforms with a flexible architecture that makes it easy to add new platforms. Each platform implementation follows a consistent pattern:

1. Platform-specific dependencies struct
2. Content pulling activity
3. Platform type registration
4. Workflow integration

### Supported Platforms

- **Reddit**
  - Supports both posts (t3*) and comments (t1*)
  - Requires OAuth2 authentication
  - Returns formatted content with author and subreddit information
- **YouTube** (placeholder)
- **Yelp** (placeholder)
- **Google** (placeholder)

### Example: Starting a Bounty Assessment Workflow for Different Platforms

```go
// For Reddit
redditDeps := rbb.RedditDependencies{
    UserAgent:    "MyApp/1.0",
    Username:     "myUsername",
    Password:     "myPassword",
    ClientID:     "myClientID",
    ClientSecret: "myClientSecret",
}

redditBountyInput := rbb.BountyAssessmentWorkflowInput{
    RequirementsDescription: "Content must be at least 500 words and discuss AI technology",
    BountyPerPost:           solana.NewUSDCAmount(5),
    TotalBounty:             solana.NewUSDCAmount(100),
    OwnerID:                 "owner123",
    SolanaWallet:            "owner_wallet_address",
    USDCAccount:             "owner_usdc_account",
    ServerURL:               "https://api.example.com",
    AuthToken:               "auth_token",
    PlatformType:            rbb.PlatformReddit,
    PlatformDependencies:    redditDeps,
    Timeout:                 24 * time.Hour,
}

// Start the Reddit bounty workflow
redditWorkflowID := "reddit-bounty-" + uuid.New().String()
_, err = client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
    ID:        redditWorkflowID,
    TaskQueue: rbb.TaskQueueName,
}, rbb.BountyAssessmentWorkflow, redditBountyInput)
```

### Content Pulling

The content pulling system is designed to be platform-agnostic while providing platform-specific implementations. Each platform's content pulling activity:

1. Handles platform-specific authentication
2. Manages API rate limits and tokens
3. Returns content in a consistent format
4. Includes metadata like author and source

Example of Reddit content pulling:

```go
// Pull a Reddit post
content, err := activities.PullRedditContent("t3_abcdef")
// Returns: "Post by u/username in r/subreddit:\nTitle: Post Title\n\nPost content"

// Pull a Reddit comment
content, err := activities.PullRedditContent("t1_abcdef")
// Returns: "Comment by u/username in r/subreddit:\nComment content"
```

### Implementing a New Platform

To add support for a new platform:

1. Define a new platform type constant in `activity.go`:

```go
const (
    PlatformNewPlatform PlatformType = "new_platform"
)
```

2. Create a new dependencies struct:

```go
type NewPlatformDependencies struct {
    // Platform-specific configuration
    APIKey     string
    APISecret  string
    // ... other fields
}
```

3. Implement the content pulling activity:

```go
func (a *Activities) PullNewPlatformContent(contentID string) (string, error) {
    // Platform-specific implementation
    // Handle authentication
    // Make API calls
    // Format response
    return formattedContent, nil
}
```

4. Update the `PullContentWorkflow` to handle the new platform:

```go
case PlatformNewPlatform:
    deps, ok := input.Dependencies.(NewPlatformDependencies)
    if !ok {
        return "", fmt.Errorf("invalid dependencies for NewPlatform")
    }
    activities.newPlatformDeps = deps
    err = workflow.ExecuteActivity(ctx, activities.PullNewPlatformContent, input.ContentID).Get(ctx, &result)
```

## Testing

The workflow tests use Temporal's test framework to verify the correct behavior of workflows and activities. Tests include:

- Mocked activity implementations
- Signal handling verification
- Timeout handling
- Error handling
- Platform-specific content pulling tests

Run the tests with:

```bash
make test-rbb
```

For workflow-specific tests:

```bash
make test-workflow
```

For testing content pulling directly:

```bash
./bin/rbb debug pull-content --platform reddit --content-id t3_abcdef --reddit-user-agent "MyApp/1.0" --reddit-username "username" --reddit-password "password" --reddit-client-id "client_id" --reddit-client-secret "client_secret"
```

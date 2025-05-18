# abb

This package implements the Temporal workflows and activities that power the Affiliate Bounty Board application.

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
  - Supports posts (t3*) and comments (t1*)
  - Requires OAuth2 authentication
  - Returns formatted content with author and subreddit information
- **YouTube** (placeholder)
- **Twitch**
  - Supports Videos (VODs/Archives) and Clips
  - Requires App Access Token (Client Credentials)
- **Hacker News**
  - Supports stories and comments
  - No authentication required
  - Returns item data including score, author, text/title/url
- **Bluesky**
  - Supports individual posts (skeets)
  - Fetches public posts via AT URI (e.g., `at://did:plc:.../app.bsky.feed.post/...`)
  - No authentication required for public posts
  - Returns post view including text, author, embeds, counts, etc.

### Example: Starting a Bounty Assessment Workflow for Different Platforms

```go
// For Reddit
redditDeps := abb.RedditDependencies{
    UserAgent:    "MyApp/1.0",
    Username:     "myUsername",
    Password:     "myPassword",
    ClientID:     "myClientID",
    ClientSecret: "myClientSecret",
}

redditBountyInput := abb.BountyAssessmentWorkflowInput{
    RequirementsDescription: "Content must be at least 500 words and discuss AI technology",
    BountyPerPost:           solana.NewUSDCAmount(5),
    TotalBounty:             solana.NewUSDCAmount(100),
    OwnerID:                 "owner123",
    SolanaWallet:            "owner_wallet_address",
    USDCAccount:             "owner_usdc_account",
    ServerURL:               "https://api.example.com",
    AuthToken:               "auth_token",
    PlatformType:            abb.PlatformReddit,
    PlatformDependencies:    redditDeps,
    Timeout:                 24 * time.Hour,
}

// Start the affiliate bounty workflow
redditWorkflowID := "reddit-bounty-" + uuid.New().String()
_, err = client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
    ID:        redditWorkflowID,
    TaskQueue: abb.TaskQueueName,
}, abb.BountyAssessmentWorkflow, redditBountyInput)

// For Hacker News (No specific dependencies needed)
hnInput := abb.BountyAssessmentWorkflowInput{
	RequirementsDescription: "Comment must provide constructive feedback",
	BountyPerPost:           solana.NewUSDCAmount(0.5), // e.g., per comment
	TotalBounty:             solana.NewUSDCAmount(50),
	OwnerID:                 "owner789",
	SolanaWallet:            "owner_hn_wallet_address",
	USDCAccount:             "owner_hn_usdc_account",
	ServerURL:               "https://api.example.com",
	AuthToken:               "auth_token",
	PlatformType:            abb.PlatformHackerNews,
	PlatformDependencies:    abb.HackerNewsDependencies{}, // Use empty struct
	Timeout:                 7 * 24 * time.Hour,
}

// Start the Hacker News bounty workflow
hnWorkflowID := "hn-bounty-" + uuid.New().String()
_, err = client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
    ID:        hnWorkflowID,
    TaskQueue: abb.TaskQueueName,
}, abb.BountyAssessmentWorkflow, hnInput)

// For Bluesky (No specific dependencies needed for public posts)
blueskyInput := abb.BountyAssessmentWorkflowInput{
	RequirementsDescription: "Post must mention AI ethics",
	BountyPerPost:           solana.NewUSDCAmount(1.0),
	TotalBounty:             solana.NewUSDCAmount(20),
	OwnerID:                 "owner_bluesky",
	SolanaWallet:            "owner_bluesky_wallet",
	USDCAccount:             "owner_bluesky_usdc",
	ServerURL:               "https://api.example.com",
	AuthToken:               "auth_token",
	PlatformType:            abb.PlatformBluesky,
	PlatformDependencies:    abb.BlueskyDependencies{}, // Empty struct for now
	Timeout:                 14 * 24 * time.Hour,
}

// Start the Bluesky bounty workflow
blueskyWorkflowID := "bluesky-bounty-" + uuid.New().String()
_, err = client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
    ID:        blueskyWorkflowID,
    TaskQueue: abb.TaskQueueName,
}, abb.BountyAssessmentWorkflow, blueskyInput)
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
redditContent, err := activities.PullRedditContent("t3_abcdef")
if err != nil {
    // Handle error
}
// Access structured data
fmt.Printf("Post by u/%s in r/%s:\nTitle: %s\n\n%s",
    redditContent.Author, redditContent.Subreddit, redditContent.Title, redditContent.Selftext)
// Or use the formatter
content := FormatRedditContent(redditContent)

// Pull a Reddit comment
redditContent, err := activities.PullRedditContent("t1_abcdef")
if err != nil {
    // Handle error
}
// Access structured data
fmt.Printf("Comment by u/%s in r/%s:\n%s",
    redditContent.Author, redditContent.Subreddit, redditContent.Body)
// Or use the formatter
content := FormatRedditContent(redditContent)

// Pull a YouTube video
youtubeContent, err := activities.PullYouTubeContent(ctx, "yt_dQw4w9WgXcQ")
if err != nil {
    // Handle error
}
// Access structured data
fmt.Printf("Video: %s\nChannel: %s\nViews: %d\nDescription: %s\n",
    youtubeContent.Title, youtubeContent.ChannelTitle, youtubeContent.ViewCount, youtubeContent.Description)
// Or use the formatter
content := FormatYouTubeContent(youtubeContent)
```

### Implementing a New Platform

To add support for a new platform (e.g., "NewPlatform"):

1. Define a new platform type constant in `activity.go`:

```go
const (
    PlatformNewPlatform PlatformType = "newplatform"
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
    // Example: Call the new activity
    // var newContent string
    // err = workflow.ExecuteActivity(ctx, (*Activities).PullNewPlatformContent, input.ContentID).Get(ctx, &newContent)
    // if err == nil {
    //    contentBytes = []byte(newContent)
    // }
    return nil, fmt.Errorf("platform handler for NewPlatform not fully implemented in PullContentWorkflow")
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
make test-abb
# Or using go test directly
go test ./... -v
```

## Environment Variables

The following environment variables are needed to run the activities:

**Solana:**

- `SOLANA_ESCROW_PRIVATE_KEY`: Base58 encoded private key for the escrow wallet.
- `SOLANA_ESCROW_WALLET`: Base58 encoded public key of the escrow wallet.
- `SOLANA_TREASURY_WALLET`: (Optional) Base58 encoded public key of the treasury wallet for fees.
- `SOLANA_USDC_MINT_ADDRESS`: Base58 encoded public key of the USDC mint.
- `SOLANA_RPC_ENDPOINT`: URL for the Solana RPC endpoint.
- `SOLANA_WS_ENDPOINT`: URL for the Solana WebSocket endpoint.

**LLM (Text Analysis):**

- `LLM_PROVIDER`: Name of the text LLM provider (e.g., `openai`).
- `LLM_API_KEY`: API key for the text LLM provider.
- `LLM_MODEL`: Specific text model to use (e.g., `gpt-4`).
- `LLM_CHECK_REQ_PROMPT_BASE`: (Optional) Base64 encoded custom base prompt for `CheckContentRequirements`.

**Note on LLM Limits:** To prevent excessive token usage, internal limits are applied:

- Content provided to `CheckContentRequirements` is limited to `MaxContentCharsForLLMCheck` (currently 20,000 characters).
- Requirements provided to `CheckContentRequirements` are limited to `MaxRequirementsCharsForLLMCheck` (currently 5,000 characters).
- Requirements used in the prompt for image analysis (`AnalyzeImageURL`) are also truncated based on `MaxRequirementsCharsForLLMCheck`.

**LLM (Image Analysis):**

- `LLM_IMAGE_PROVIDER`: Name of the image LLM provider (e.g., `openai`).
- `LLM_IMAGE_API_KEY`: API key for the image LLM provider.
- `LLM_IMAGE_MODEL`: Specific image model to use (e.g., `gpt-4-vision-preview`).

**Reddit:**

- `REDDIT_USER_AGENT`: Custom User-Agent string for Reddit API requests.
- `REDDIT_USERNAME`: Reddit account username.
- `REDDIT_PASSWORD`: Reddit account password.
- `REDDIT_CLIENT_ID`: Reddit App Client ID.
- `REDDIT_CLIENT_SECRET`: Reddit App Client Secret.
- `REDDIT_FLAIR_ID`: Flair ID for bounty announcements.

**YouTube:**

- `YOUTUBE_API_KEY`: Google API Key with YouTube Data API v3 enabled.
- `YOUTUBE_APP_NAME`: Application name registered with Google Cloud.

**Twitch:**

- `TWITCH_CLIENT_ID`: Twitch Application Client ID.
- `TWITCH_CLIENT_SECRET`: Twitch Application Client Secret.

**Hacker News:**

- _No specific environment variables required for Hacker News._

**Bluesky:**

- _No specific environment variables required for fetching public posts currently._
- _(Future: May require `BLUESKY_HANDLE` and `BLUESKY_APP_PASSWORD` for authenticated actions)._

**Server/Auth:**

- `ABB_API_ENDPOINT`: URL of the Affiliate Bounty Board server API (replaces `SERVER_ENDPOINT` and `ABB_SERVER_URL`).
- `ABB_AUTH_TOKEN`: Authentication token for server communication (if required by activities, replaces `AUTH_TOKEN`).
- `ABB_SECRET_KEY`: Secret key used by the periodic bounty publisher for authentication with the ABB server.
- `ABB_PUBLIC_BASE_URL`: Publicly accessible base URL for the server (replaces `PUBLIC_BASE_URL`).
- `ENV`: The deployment environment (e.g., "dev", "prod"). Used for search attributes and schedule IDs.

**Periodic Publisher:**

- `PUBLISH_TARGET_SUBREDDIT`: The subreddit where the periodic publisher should post bounties.
- `PERIODIC_PUBLISHER_SCHEDULE_ID`: (Optional) Temporal Schedule ID for the publisher.
- `PERIODIC_PUBLISHER_SCHEDULE_INTERVAL`: (Optional) Interval for the periodic publisher (e.g., "24h").

**Email Sending:**

- `EMAIL_PROVIDER`: The email service provider to use (e.g., "sendgrid", "ses").
- `EMAIL_API_KEY`: API key for the chosen email service provider.
- `EMAIL_SENDER`: The email address from which emails will be sent (e.g., "noreply@yourdomain.com").

**Debugging/Local Testing:**

- Consider using a `.env` file and a tool like `godotenv` for local development.
- For Solana, use the devnet or testnet endpoints and wallets.

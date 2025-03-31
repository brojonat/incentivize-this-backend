# reddit-bounty-board

I was watching this new show on Apple with Seth Rogan where he has to make a cinematic Koolaid movie. This got me thinking about advertising on Reddit. Advertisers would pay for favorable mentions of their product. We can facilitate that. This is a collection of Temporal workflows and activities that provide a sort of "bounty" board for making posts on Reddit. Here's the flow:

1. Advertiser specifies condition of post, a rate per post, and provides a total bounty to our escrow. (e.g., mention Koolaid in a positive light, $0.50/post, $500).
2. A user scrolling our board sees the bounty and makes the post.
3. The user sends us the post_id.
4. We analyze the post with an LLM to determine if the post satisfies the requirements. If so, we pay out the bounty.
5. Reddit can try an get mad at us, but it drives engagement with their platform, and we're not writing anything ourselves. Plus, we don't need to make that many queries ourselves; we only need to verify users posts. In other words, we don't have to discover them because the users do the work for us.

## Environment Setup

The project uses environment variables for configuration. Create a `.env.server` file with the following variables:

```bash
SERVER_SECRET_KEY=your_secret_key
SERVER_ENDPOINT=http://localhost:8080
SERVER_PORT=8080
AUTH_TOKEN=your_bearer_token  # Will be set by the CLI
```

Load the environment variables using:

```bash
set -o allexport && source .env.server && set +o allexport
```

## Authentication

The project uses a two-step authentication process:

1. **Basic Auth**: Used only for the `/token` endpoint to obtain a Bearer token

   - Username: Your email
   - Password: Server secret key (`SERVER_SECRET_KEY`)

2. **Bearer Token**: Used for all other authenticated endpoints
   - Obtained from the `/token` endpoint
   - Passed in the `Authorization: Bearer <token>` header

## CLI Usage

### Server Commands

Start the HTTP server:

```bash
rbb run http-server
```

Required env vars:

- `SERVER_PORT`: Port to listen on (default: 8080)
- `SERVER_SECRET_KEY`: For validating authentication

### Admin Commands

Get a new Bearer token:

```bash
rbb admin auth get-token --email your@email.com [--env-file .env.server]
```

Required env vars:

- `SERVER_ENDPOINT`: Server URL (default: http://localhost:8080)
- `SERVER_SECRET_KEY`: For Basic auth

Options:

- `--email`: Your email address (required)
- `--env-file`: Path to env file to update with new token
- `--endpoint`: Override server endpoint
- `--secret-key`: Override server secret key

**Note**: A running server is required to generate new tokens. Make sure to start the server in another terminal using `rbb run http-server` before attempting to get a new token.

## HTTP API Routes

### Authentication Endpoints

- `POST /token`

  - Get a new Bearer token
  - Requires Basic auth
  - Response:
    ```json
    {
      "message": "Bearer <token>"
    }
    ```

- `GET /ping`
  - Health check endpoint
  - Requires Bearer token
  - Response:
    ```json
    {
      "message": "ok"
    }
    ```

### Advertiser Endpoints

- `POST /api/bounties`

  - Create a new bounty
  - Body:
    ```json
    {
      "description": "Mention Koolaid in a positive light",
      "amount": 0.5,
      "requirements": [
        "Content must include the keyword 'Koolaid'",
        "Content must have a positive sentiment",
        "Content must have at least 50 words"
      ]
    }
    ```

- `GET /api/bounties`

  - List all active bounties
  - Query params: `page`, `limit`, `status`

- `GET /api/bounties/:id`
  - Get details of a specific bounty

### User Endpoints

- `POST /api/submissions`

  - Submit a Reddit post for bounty verification
  - Body:
    ```json
    {
      "bountyId": "123",
      "postId": "abc123",
      "redditUsername": "user123"
    }
    ```

- `GET /api/submissions/:id`
  - Check status of a submission

### Admin Endpoints

- `PUT /api/bounties/:id/status`

  - Update bounty status (active/paused/completed)
  - Requires admin authentication

## Architecture

The backend is an HTTP server written in Go that provides the API endpoints and orchestrates the Temporal workflows.

## Environment Variables Reference

| Variable               | Description                                     | Used By      | Default               |
| ---------------------- | ----------------------------------------------- | ------------ | --------------------- |
| SERVER_SECRET_KEY      | Server's secret key for auth                    | All commands | None                  |
| SERVER_ENDPOINT        | HTTP server endpoint                            | CLI commands | http://localhost:8080 |
| SERVER_PORT            | Port for HTTP server                            | http-server  | 8080                  |
| AUTH_TOKEN             | Bearer token for auth                           | CLI commands | None                  |
| USER_REVENUE_SHARE_PCT | Percentage of advertising revenue paid to users | http-server  | 50                    |

## Development Setup

### Prerequisites

- Go 1.21 or later
- Temporal CLI and server
- OpenAI API key (for LLM integration)

### Local Development

1. Start Temporal server:

```bash
temporal server start-dev
```

2. Start the backend server:

```bash
rbb run http-server
```

### Testing

The project includes comprehensive test suites for various components. Use the following Makefile targets to run tests:

```bash
# Run all tests
make test

# Run only workflow-related tests
make test-workflow

# Run tests for specific packages
make test-solana
make test-rbb

# Generate test coverage report (HTML)
make test-coverage

# Show coverage summary in terminal
make test-coverage-summary

# Run tests optimized for CI environments (with race detection)
make test-ci
```

Test coverage reports are generated in the project root as `coverage.out` (raw data) and `coverage.html` (HTML report).

## Temporal Workflows

The system uses Temporal for orchestrating the bounty verification process:

### Workflows

1. **BountyCreationWorkflow**

   - Handles the creation and initialization of new bounties
   - Sets up escrow and initializes verification parameters

2. **SubmissionVerificationWorkflow**
   - Orchestrates the verification of user submissions
   - Coordinates with LLM for content analysis
   - Handles payment distribution

### Activities

1. **RedditActivities**

   - Fetch post content
   - Verify post existence and ownership
   - Check post metrics

2. **LLMActivities**

   - Analyze post content
   - Verify requirements compliance
   - Generate verification reports

3. **PaymentActivities**
   - Handle escrow management
   - Process payments to users
   - Track payment status

## LLM Integration

The system uses OpenAI's GPT models for content analysis:

- Verifies post content against bounty requirements
- Analyzes sentiment and context
- Checks for keyword presence and usage
- Generates detailed verification reports

Required environment variables:

```bash
OPENAI_API_KEY=your_api_key
OPENAI_MODEL=gpt-4  # or preferred model
```

## Deployment Considerations

### Backend Deployment

1. Set up a production Temporal cluster
2. Configure proper environment variables
3. Set up monitoring and logging
4. Configure rate limiting and security measures

### Security Considerations

- Use HTTPS in production
- Implement proper rate limiting
  - API endpoints: 100 requests per minute per IP
  - Authentication endpoints: 5 attempts per minute per IP
  - Submission endpoints: 10 submissions per hour per user
  - Bounty creation: 5 bounties per day per advertiser
  - Consider implementing a token bucket algorithm for smooth rate limiting
  - Use Redis or similar for distributed rate limiting in production
- Set up monitoring and alerting
- Regular security audits
- Proper secret management

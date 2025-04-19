# affiliate-bounty-board

I was watching this new show on Apple with Seth Rogan where he has to make a cinematic Koolaid movie. This got me thinking about advertising on Reddit. Advertisers would pay for favorable mentions of their product. We can facilitate that. This is a collection of Temporal workflows and activities that provide a sort of "bounty" board for making posts on Reddit. This generalizes pretty well to other platforms too, namely, YouTube. Here's the flow:

1. Advertiser creates a bounty with a reward amount and stipulations.
2. Content creator sees the bounty.
3. Content creator fulfills the bounty.
4. We assess the content.
5. If approved, content creator gets paid from the escrow.

## Development

### Prerequisites

- Go 1.24 or later
- Docker
- Kubernetes cluster
- kubectl configured to access your cluster
- Temporal server running (can be local or remote)
- Make

### Environment Setup

1. Copy the example environment files:

   ```bash
   cp worker/.env.example worker/.env.prod
   cp server/.env.server.example server/.env.server.prod
   ```

2. Update the environment files with your configuration:
   - Set up your database credentials
   - Configure Temporal connection details
   - Configure Solana credentials
   - Configure individual platform credentials

### Local Development

The recommended way to run the server and worker locally for development is using the integrated tmux development session. This provides separate panes for the server, worker, Temporal port-forwarding, and a CLI with environment variables automatically sourced.

**Prerequisites for Dev Session:**

- Ensure `tmux` is installed (`brew install tmux` on macOS).
- Ensure you have configured `kubectl` access to a Kubernetes cluster where Temporal is running.

**Running the Dev Session:**

1.  **Build the CLI and run tests:**

    ```bash
    make build-cli
    make test
    ```

2.  **Start the Session:**

    ```bash
    make start-dev-session
    ```

    This will create/attach to a tmux session named `abb-dev`.

3.  **Stopping the Session:**
    Detach from tmux normally (`Ctrl+b`, `d`) or run:
    ```bash
    make stop-dev-session
    ```

_(For details on the specific processes running in the session and how to test the bounty workflow within it, see the "Testing the Bounty Workflow Locally" section below.)_

### Testing the Bounty Workflow Locally

This flow demonstrates how to create a bounty, fund it using the CLI utility (simulating an advertiser payment), and trigger the workflow locally using the `dev-session`.

1.  **Start the Development Session:**
    This launches tmux with the necessary components (server, worker, port-forwarding) and sources `.env.server.debug` in the CLI pane.

    ```bash
    make start-dev-session
    ```

2.  **Create the Bounty (in the tmux CLI pane):**
    Run the `bounty create` command. Note the `Workflow started: <WORKFLOW_ID>` message in the output.

    ```bash
    # Example:
    ./bin/abb admin bounty create -r "foo" -r "bar" -r "baz" --per-post 0.01 --total 0.1 --platform reddit --payment-timeout 10m
    # Output will include: Workflow started: bounty-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
    ```

3.  **Prepare Funder Private Key (CLI pane):**
    Ensure the funder wallet configured in `.env.server.debug` (`SOLANA_TEST_FUNDER_PRIVATE_KEY`) has Devnet SOL for fees and the correct Devnet USDC (matching `SOLANA_USDC_MINT_ADDRESS`) for the bounty amount. You can export the private key for use in the next step (or just have it ready).

    ```bash
    # Example using print-private-key utility (assuming key is in a file)
    export SOLANA_TEST_FUNDER_PRIVATE_KEY=$(./bin/abb admin util print-private-key -k ~/.config/solana/test-funder.json)
    # Or, if SOLANA_TEST_FUNDER_PRIVATE_KEY is already set in .env.server.debug, it's sourced by dev-session.
    ```

4.  **Fund the Bounty (CLI pane):**
    Use the `fund-escrow` command. Crucially, you must provide:

    - The _original_ total bounty amount (`-a 0.1` in this example).
    - The specific `workflowID` (`-w <WORKFLOW_ID>`) obtained from step 2. This links the payment to the correct bounty workflow via the transaction memo.

    ```bash
    # Replace {bounty_workflow_id} with the actual ID from step 2
    ./bin/abb admin util fund-escrow -a 0.1 -w {bounty_workflow_id}
    ```

5.  **Observe Verification (in the tmux Worker pane):**
    The worker logs (`logs/worker.log`) should show the `VerifyPayment` activity polling for transactions. Once it finds the funding transaction matching the amount and the workflow ID in the memo, it will log `Matching payment transaction found` and `Payment verified successfully`, allowing the workflow to proceed.

    This process allows you to test the full bounty lifecycle, including the crucial payment verification step, in your local development environment.

### Key Makefile Targets

Here are some of the most frequently used Makefile targets:

- `make build-cli`: Builds the `abb` command-line interface executable.
- `make test`: Runs all unit tests.
- `make test-coverage`: Runs tests and generates an HTML coverage report.
- `make run-http-server-local`: Runs the HTTP server locally (requires Temporal and port-forwarding).
- `make run-worker-local`: Runs the Temporal worker locally (requires Temporal and port-forwarding).
- `make dev-session`: Starts a tmux session with the server, worker, and a CLI pane for easy local development. It handles port-forwarding automatically. Requires `tmux`.
- `make stop-dev-session`: Stops the tmux development session and associated processes.
- `make deploy-all`: Builds and deploys both the server and worker to the configured Kubernetes cluster.
- `make logs-server`/`make logs-worker`: Tails logs for the deployed server/worker pods.
- `make status`: Shows the status of Kubernetes deployments and pods.

Refer to the `Makefile` for the complete list and details.

## Deployment

### Prerequisites

- Kubernetes cluster with:
  - nginx-ingress controller
  - cert-manager for SSL certificates
  - A default StorageClass for persistent volumes
- Docker registry access
- kubectl configured to access your cluster

### Environment Setup

1. Set up your environment files:

   ```bash
   cp worker/.env.example worker/.env.prod
   cp server/.env.server.example server/.env.server.prod
   ```

2. Update the environment files with production values:
   - Database credentials
   - Temporal connection details
   - API keys and secrets
   - Other environment-specific configurations

### Building and Pushing Images

1. Build and push the CLI image:
   ```bash
   make build-push-cli
   ```

### Deploying to Kubernetes

1. Deploy both server and worker:

   ```bash
   make deploy-all
   ```

2. Or deploy components individually:

   ```bash
   # Deploy server only
   make deploy-server

   # Deploy worker only
   make deploy-worker
   ```

3. Verify the deployment:
   ```bash
   make status
   ```

### Managing Deployments

1. View logs:

   ```bash
   # Server logs
   make logs-server

   # Worker logs
   make logs-worker
   ```

2. Update secrets without redeploying:

   ```bash
   # Update server secrets
   make update-secrets-server

   # Update worker secrets
   make update-secrets-worker
   ```

3. Restart deployments:

   ```bash
   # Restart server
   make restart-server

   # Restart worker
   make restart-worker
   ```

4. Debug deployments:

   ```bash
   # View detailed server information
   make describe-server

   # View detailed worker information
   make describe-worker
   ```

5. Port forwarding for local access:
   ```bash
   make port-forward-server
   ```

### Cleanup

To remove all deployed resources:

```bash
make delete-all
```

Or remove components individually:

```bash
# Remove server only
make delete-server

# Remove worker only
make delete-worker
```

## Architecture

The application consists of two main components:

1. **Server**: HTTP API server that handles:

   - User authentication/token management
   - Bounty CRUDL endpoints

2. **Worker**: Temporal worker that:
   - Processes bounty workflows
   - Handles content assessment
   - Manages payment distribution

### System Architecture Diagram

![System Architecture](docs/architecture.svg)

The diagram above illustrates the flow of data and interactions between different components of the system:

- **Advertisers** create bounties through the HTTP Server
- The **HTTP Server** orchestrates the workflow by communicating with the Temporal Server
- The **Temporal Server** manages the workflow execution
- The **Worker** processes the workflow and interacts with Reddit
- **Content Creators** submit their posts to Reddit
- The system verifies the content and processes payments

## Contributing

1. Fork the repository
2. Create your feature branch
3. Commit your changes
4. Push to the branch
5. Create a new Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Environment Setup

Load the environment variables using:

```bash
set -o allexport && source .env.server && set +o allexport
```

## Authentication

The project uses a two-step authentication process typical to what you'd see in a OAuth2 flow:

1. **Form with Credentials**: Used only for the `POST /token` endpoint to obtain a Bearer token

   - Client submits form data with the following fields
     - `username`: Your email
     - `password`: Server secret key (`SERVER_SECRET_KEY`)

2. **Bearer Token**: Used for all other authenticated endpoints
   - Obtained from the `/token` endpoint
   - Passed in the `Authorization: Bearer <token>` header

## CLI Usage

### Server Commands

Start the HTTP server:

```bash
abb run http-server
```

Required env vars:

- `SERVER_PORT`: Port to listen on (default: 8080)
- `SERVER_SECRET_KEY`: For validating authentication

### Admin Commands

Get a new Bearer token:

```bash
abb admin auth get-token --email your@email.com [--env-file .env.server]
```

Required env vars:

- `SERVER_ENDPOINT`: Server URL (default: http://localhost:8080)
- `SERVER_SECRET_KEY`: For Basic auth

Options:

- `--email`: Your email address (required)
- `--env-file`: Path to env file to update with new token
- `--endpoint`: Override server endpoint
- `--secret-key`: Override server secret key

**Note**: A running server is required to generate new tokens. Make sure to start the server in another terminal using `abb run http-server` before attempting to get a new token.

### Content Assessment Examples

The CLI provides commands for pulling and assessing content from various platforms:

#### Pulling Content

```bash
# Pull a Reddit post
./bin/abb debug pull-content \
  --platform reddit \
  --content-id t3_1johy3a \
  --reddit-user-agent "YourApp/1.0" \
  --reddit-username "your_username" \
  --reddit-password "your_password" \
  --reddit-client-id "your_client_id" \
  --reddit-client-secret "your_client_secret"

# Pull a YouTube video
./bin/abb debug pull-content \
  --platform youtube \
  --content-id yt_dQw4w9WgXcQ \
  --youtube-api-key "your_api_key" \
  --youtube-app-name "YourApp"
```

#### Assessing Content

You can pipe the output from `pull-content` directly into `check-requirements`:

```bash
# Assess a Reddit post
./bin/abb debug pull-content \
  --platform reddit \
  --content-id t3_1johy3a \
  --reddit-user-agent "YourApp/1.0" \
  --reddit-username "your_username" \
  --reddit-password "your_password" \
  --reddit-client-id "your_client_id" \
  --reddit-client-secret "your_client_secret" | \
./bin/abb debug check-requirements \
  --content - \
  --requirement "Content must be at least 500 words" \
  --requirement "Must discuss AI technology" \
  --requirement "Must be in English"

# Assess a YouTube video
./bin/abb debug pull-content \
  --platform youtube \
  --content-id yt_dQw4w9WgXcQ \
  --youtube-api-key "your_api_key" \
  --youtube-app-name "YourApp" | \
./bin/abb debug check-requirements \
  --content - \
  --requirement "Video must be at least 10 minutes long" \
  --requirement "Must include captions" \
  --requirement "Must be in English"
```

The `check-requirements` command supports additional parameters:

- `--openai-api-key`: Your OpenAI API key
- `--openai-model`: Model to use (defaults to "gpt-4")
- `--max-tokens`: Maximum tokens to generate (defaults to 1000)
- `--temperature`: Temperature for text generation (defaults to 0.7)

The output will be JSON containing:

```json
{
  "satisfies": true,
  "reason": "The content meets all requirements..."
}
```

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
abb run http-server
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

## Solana Wallet Management

These steps outline how to generate a new Solana keypair, fund it on devnet/testnet, and retrieve its private key using the `abb` CLI.

### 1. Generate a New Keypair

Use the Solana CLI tool to create a new keypair file. Choose a memorable path, for example, `~/.config/solana/my_new_wallet.json`.

```bash
# Install Solana tools if you haven't already: https://docs.solana.com/cli/install
solana-keygen new --outfile ~/.config/solana/my_new_wallet.json
```

This command will output the public key (wallet address) and save the keypair to the specified file. Keep this file secure.

### 2. Fund the Wallet (Airdrop)

You can request SOL tokens for your new wallet on networks like devnet or testnet using the `solana airdrop` command. Replace `<YOUR_NEW_WALLET_ADDRESS>` with the public key generated in the previous step.

```bash
# Make sure your Solana CLI is configured for the desired network (e.g., devnet)
# solana config set --url https://api.devnet.solana.com

# Request an airdrop (amount may vary by network)
solana airdrop 2 <YOUR_NEW_WALLET_ADDRESS>
```

Wait for the transaction to confirm. You can check the balance with `solana balance <YOUR_NEW_WALLET_ADDRESS>`.

### 3. Print the Private Key

Use the `abb` CLI utility to print the private key in base58 format from your keypair file. This is useful if you need to import the wallet into another application or service.

```bash
# First, ensure the CLI is built
make build-cli

# Print the private key
./bin/abb util print-private-key --keypair-path ~/.config/solana/my_new_wallet.json
```

If your keypair is located at the default path (`~/.config/solana/id.json`), you can omit the `--keypair-path` flag:

```bash
./bin/abb util print-private-key
```

**Important:** Handle your private keys with extreme care. Never commit them to version control or share them publicly.

### 4. Finding Token Accounts and Checking Balances (spl-token)

Unlike SOL which is held directly by your main wallet address, SPL Tokens (like USDC) are held in separate Associated Token Accounts (ATAs) linked to your main wallet. You often need to find the address of an ATA or check its token balance.

The `spl-token` command-line utility is essential for this. (You might need to install the Solana Tool Suite if you don't have it: `sh -c "$(curl -sSfL https://release.solana.com/v1.18.4/install)"` - check Solana docs for latest).

**Finding an Associated Token Account (ATA) Address:**

To find the specific ATA address for a given owner wallet and a specific token mint:

```bash
# Replace <TOKEN_MINT_ADDRESS> with the mint (e.g., Devnet USDC)
# Replace <OWNER_WALLET_ADDRESS> with the main wallet address

spl-token address --token <TOKEN_MINT_ADDRESS> --owner <OWNER_WALLET_ADDRESS>

# Example for Native Devnet USDC (Gh9Zw...):
spl-token address --token Gh9ZwEmdLJ8DscKNTkTqPbNwLNNBjuSzaG9Vp2KGtKJr --owner AuQiyiWqPVHYhv9emCGfZm6oWaic4ojBHzqK4cv6Np4V

# Example for Wrapped Devnet USDC (4zMMC...):
spl-token address --token 4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU --owner AuQiyiWqPVHYhv9emCGfZm6oWaic4ojBHzqK4cv6Np4V
```

This command prints the derived ATA address.

**Checking the Token Balance of an Account:**

Once you know the address of a specific token account (like an ATA), you can check its balance:

```bash
# Replace <TOKEN_ACCOUNT_ADDRESS> with the ATA address you found
# Replace <RPC_ENDPOINT> with your Devnet RPC URL (e.g., https://api.devnet.solana.com)

spl-token balance --address <TOKEN_ACCOUNT_ADDRESS> --url <RPC_ENDPOINT>

# Example checking the wrapped USDC ATA derived above:
spl-token balance --address 5U7XDWrusNB6zTGZ8dazJsu67MDzWMco3WGKFYkiLjt1 --url https://api.devnet.solana.com
```

This command shows the balance of the specific SPL Token held by that account address. Remember that the ATA itself usually holds 0 SOL; its SOL balance is irrelevant.

# Test Scripts

This directory contains test scripts for various components of the Affiliate Bounty Board application.

## Content Pulling Tests

The `test_pull_content.sh` script helps you test the content pulling functionality for different platforms.

### Prerequisites

1. Make sure you have the required environment variables set:

   ```bash
   export REDDIT_USER_AGENT='YourApp/1.0'
   export REDDIT_USERNAME='your_username'
   export REDDIT_PASSWORD='your_password'
   export REDDIT_CLIENT_ID='your_client_id'
   export REDDIT_CLIENT_SECRET='your_client_secret'
   ```

2. Ensure all components are built:
   ```bash
   make build-worker
   make build-server
   make build-cli
   ```

### Running the Tests

1. Make the script executable:

   ```bash
   chmod +x test_pull_content.sh
   ```

2. Run the test script:
   ```bash
   ./test_pull_content.sh
   ```

The script will:

- Check for required environment variables
- Test pulling content for both posts and comments
- Display the results of each test
- Remind you to rebuild components if you make changes

### Example Output

```
⚠️  REMINDER: Make sure you've rebuilt all components after making changes:
   make build-worker
   make build-server
   make build-cli

🔍 Testing content pull for ID: t3_mkrx5a1
----------------------------------------
{
  "platform": "reddit",
  "content_id": "t3_mkrx5a1",
  "content": "Post by u/username in r/subreddit:\nTitle: Post Title\n\nPost content"
}
----------------------------------------

🔍 Testing content pull for ID: t1_mkrx5a1
----------------------------------------
{
  "platform": "reddit",
  "content_id": "t1_mkrx5a1",
  "content": "Comment by u/username in r/subreddit:\nComment content"
}
----------------------------------------

✅ Test script completed!

⚠️  REMINDER: If you make any changes to the code, remember to rebuild:
   make build-worker
   make build-server
   make build-cli
```

### Troubleshooting

1. If you get "invalid dependencies for Reddit platform" error:

   - Make sure you've rebuilt the worker after making changes
   - Check that all environment variables are set correctly

2. If you get a 404 error:

   - Verify that the content ID is correct
   - Make sure you have the necessary permissions to access the content

3. If you get authentication errors:
   - Double-check your Reddit credentials
   - Ensure your Reddit app has the correct permissions

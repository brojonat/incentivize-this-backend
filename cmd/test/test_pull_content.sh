#!/bin/bash

# REMINDER: Always rebuild the worker, server, and CLI after making changes to the code
echo "⚠️  REMINDER: Make sure you've rebuilt all components after making changes:"
echo "   make build-worker"
echo "   make build-server"
echo "   make build-cli"
echo ""

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Check if required environment variables are set
check_env_vars() {
    local missing_vars=()
    local required_vars=(
        "REDDIT_USER_AGENT"
        "REDDIT_USERNAME"
        "REDDIT_PASSWORD"
        "REDDIT_CLIENT_ID"
        "REDDIT_CLIENT_SECRET"
    )

    for var in "${required_vars[@]}"; do
        if [ -z "${!var}" ]; then
            missing_vars+=("$var")
        fi
    done

    if [ ${#missing_vars[@]} -ne 0 ]; then
        echo "❌ Missing required environment variables:"
        printf '%s\n' "${missing_vars[@]}"
        echo ""
        echo "Please set these variables in your environment or create a .env file:"
        echo "export REDDIT_USER_AGENT='YourApp/1.0'"
        echo "export REDDIT_USERNAME='your_username'"
        echo "export REDDIT_PASSWORD='your_password'"
        echo "export REDDIT_CLIENT_ID='your_client_id'"
        echo "export REDDIT_CLIENT_SECRET='your_client_secret'"
        exit 1
    fi
}

# Check if required environment variables are set
check_env_vars

# Test cases - using real, accessible Reddit content
test_cases=(
    "t3_1johy3a"  # A Reddit post
    "t1_mkrx5a1"  # A Reddit comment
)

# Run tests
for content_id in "${test_cases[@]}"; do
    echo "🔍 Testing content pull for ID: $content_id"
    echo "----------------------------------------"

    # Run the command and capture both output and exit code
    output=$(./bin/rbb debug pull-content \
        --platform reddit \
        --content-id "$content_id" \
        --reddit-user-agent "$REDDIT_USER_AGENT" \
        --reddit-username "$REDDIT_USERNAME" \
        --reddit-password "$REDDIT_PASSWORD" \
        --reddit-client-id "$REDDIT_CLIENT_ID" \
        --reddit-client-secret "$REDDIT_CLIENT_SECRET" 2>&1)
    exit_code=$?

    # Print the output
    echo "$output"

    # Check if the command was successful
    if [ $exit_code -eq 0 ]; then
        echo "✅ Test passed for $content_id"
    else
        echo "❌ Test failed for $content_id"
        echo "Exit code: $exit_code"
    fi

    echo "----------------------------------------"
    echo ""
done

echo "✅ Test script completed!"
echo ""
echo "⚠️  REMINDER: If you make any changes to the code, remember to rebuild:"
echo "   make build-worker"
echo "   make build-server"
echo "   make build-cli"
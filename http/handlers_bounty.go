package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Rhymond/go-money"
	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"github.com/brojonat/affiliate-bounty-board/solana"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
)

// PayBountyRequest represents the request body for paying a bounty
type PayBountyRequest struct {
	Amount float64 `json:"amount"` // Amount to pay in USDC
	Wallet string  `json:"wallet"` // Destination wallet address
}

// ReturnBountyToOwnerRequest represents the request body for returning a bounty to the owner
type ReturnBountyToOwnerRequest struct {
	UserID       string  `json:"user_id"`
	Amount       float64 `json:"amount"`
	SolanaWallet string  `json:"solana_wallet"`
	USDCAccount  string  `json:"usdc_account"`
}

// CreateBountyRequest represents the request body for creating a new bounty
type CreateBountyRequest struct {
	Title             string   `json:"title"`
	Requirements      []string `json:"requirements"`
	BountyPerPost     float64  `json:"bounty_per_post"`
	TotalBounty       float64  `json:"total_bounty"`
	FeePercentage     *float64 `json:"fee_percentage,omitempty"`
	TimeoutDuration   string   `json:"timeout_duration"`
	Tier              string   `json:"tier,omitempty"`
	MaxPayoutsPerUser *int     `json:"max_payouts_per_user,omitempty"`
}

// PaidBountyItem represents a single paid bounty in the list response
type PaidBountyItem struct {
	Signature            string    `json:"signature"`
	Timestamp            time.Time `json:"timestamp"`
	RecipientOwnerWallet string    `json:"recipient_owner_wallet"`
	Amount               float64   `json:"amount"`         // Amount in USDC
	Memo                 string    `json:"memo,omitempty"` // Transaction memo, if any
}

// AssessContentRequest represents the request body for assessing content against requirements
type AssessContentRequest struct {
	BountyID     string           `json:"bounty_id"`
	ContentID    string           `json:"content_id"`
	PayoutWallet string           `json:"payout_wallet"`
	Platform     abb.PlatformKind `json:"platform"`
	ContentKind  abb.ContentKind  `json:"content_kind"`
}

// AssessContentResponse represents the response from content assessment
type AssessContentResponse struct {
	Satisfies bool   `json:"satisfies"`
	Reason    string `json:"reason"`
}

// handleCreateBounty handles the creation of a new bounty and starts a workflow
func handleCreateBounty(
	logger *slog.Logger,
	tc client.Client,
	llmProvider abb.LLMProvider,
	llmEmbedProvider abb.LLMEmbeddingProvider,
	defaultFeePercentage float64,
	env string,
	prompts struct {
		InferBountyTitle   string
		InferContentParams string
		ContentModeration  string
		HardenBounty       string
	},
) http.HandlerFunc {
	// Define a local struct for parsing the LLM's JSON response for content parameters
	type inferredContentParamsRequest struct {
		PlatformKind string `json:"PlatformKind"`
		ContentKind  string `json:"ContentKind"`
		Error        string `json:"Error,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateBountyRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}

		// Validate request
		if len(req.Requirements) == 0 {
			writeBadRequestError(w, fmt.Errorf("requirements is required"))
			return
		}
		requirementsStr := strings.Join(req.Requirements, "\n")
		if requirementsStr == "" {
			writeBadRequestError(w, fmt.Errorf("requirements cannot be empty"))
			return
		}
		if req.BountyPerPost <= 0 {
			writeBadRequestError(w, fmt.Errorf("bounty_per_post must be greater than 0"))
			return
		}
		if req.TotalBounty <= 0 {
			writeBadRequestError(w, fmt.Errorf("total_bounty must be greater than 0"))
			return
		}

		// --- Content Moderation for non-sudo users ---
		claims, isAuthed := r.Context().Value(ctxKeyJWT).(*authJWTClaims)
		if isAuthed && claims.Status < UserStatusSudo {
			type contentModerationResponse struct {
				IsAcceptable bool   `json:"is_acceptable"`
				Reason       string `json:"reason,omitempty"`
				Error        string `json:"error,omitempty"`
			}
			schema := map[string]interface{}{
				"name":   "content_moderation",
				"strict": true,
				"schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"is_acceptable": map[string]interface{}{
							"type":        "boolean",
							"description": "Whether the content is acceptable or not.",
						},
						"reason": map[string]interface{}{
							"type":        "string",
							"description": "The reason why the content is not acceptable. Leave blank if it is acceptable.",
						},
						"error": map[string]interface{}{
							"type":        "string",
							"description": "An optional error message if moderation cannot be determined. Leave blank if content is acceptable.",
						},
					},
					"required":             []string{"is_acceptable", "reason", "error"},
					"additionalProperties": false,
				},
			}
			prompt := fmt.Sprintf(prompts.ContentModeration, requirementsStr)
			schemaJSON, err := json.Marshal(schema)
			if err != nil {
				writeInternalError(logger, w, fmt.Errorf("failed to marshal content moderation schema: %w", err))
				return
			}
			resp, err := llmProvider.GenerateResponse(r.Context(), "content_moderation", prompt, schemaJSON)
			if err != nil {
				writeInternalError(logger, w, fmt.Errorf("failed to moderate content: %w", err))
				return
			}
			var moderationResp contentModerationResponse
			if err := json.Unmarshal([]byte(resp), &moderationResp); err != nil {
				writeInternalError(logger, w, fmt.Errorf("failed to parse moderation response: %w", err))
				return
			}
			if moderationResp.Error != "" {
				logger.Warn("Could not moderate content", "error", moderationResp.Error)
				writeBadRequestError(w, fmt.Errorf("could not moderate content: %s", moderationResp.Error))
				return
			}
			if !moderationResp.IsAcceptable {
				writeBadRequestError(w, fmt.Errorf("content is not acceptable: %s", moderationResp.Reason))
				return
			}
		}
		// --- End Content Moderation ---

		// --- Tier Processing ---
		var bountyTier abb.BountyTier
		if req.Tier != "" {
			tier, valid := abb.FromString(req.Tier)
			if !valid {
				writeBadRequestError(w, fmt.Errorf("invalid tier specified: '%s'", req.Tier))
				return
			}
			bountyTier = tier
		} else {
			bountyTier = abb.DefaultBountyTier
		}
		// --- End Tier Processing ---

		// --- Title Processing ---
		bountyTitle := req.Title
		if bountyTitle == "" {
			type inferredTitleRequest struct {
				Title string `json:"title"`
				Error string `json:"error,omitempty"`
			}
			schema := map[string]interface{}{
				"name":   "infer_bounty_title",
				"strict": true,
				"schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title": map[string]interface{}{
							"type":        "string",
							"description": "A concise, descriptive title for the bounty based on its requirements.",
						},
						"error": map[string]interface{}{
							"type":        "string",
							"description": "An optional error message if a title cannot be inferred. Leave blank if a title can be inferred.",
						},
					},
					// these are needed because we have "strict" set to true
					"required":             []string{"title", "error"},
					"additionalProperties": false,
				},
			}
			schemaJSON, err := json.Marshal(schema)
			if err != nil {
				writeInternalError(logger, w, fmt.Errorf("failed to marshal infer bounty title schema: %w", err))
				return
			}
			resp, err := llmProvider.GenerateResponse(r.Context(), prompts.InferBountyTitle, requirementsStr, schemaJSON)
			if err != nil {
				writeInternalError(logger, w, fmt.Errorf("failed to infer bounty title: %w", err))
				return
			}
			var inferredTitle inferredTitleRequest
			if err := json.Unmarshal([]byte(resp), &inferredTitle); err != nil {
				writeInternalError(logger, w, fmt.Errorf("failed to parse inferred title response: %w", err))
				return
			}
			if inferredTitle.Error != "" {
				logger.Warn("Could not infer title", "error", inferredTitle.Error, "title", inferredTitle.Title)
				writeBadRequestError(w, fmt.Errorf("could not infer title: %s", inferredTitle.Error))
				return
			}
			bountyTitle = inferredTitle.Title
		}
		// --- End Title Processing ---

		// --- Requirements Length Check ---
		if len(requirementsStr) > abb.MaxRequirementsCharsForLLMCheck {
			warnMsg := fmt.Sprintf("Total length of requirements exceeds maximum limit (%d > %d)", len(requirementsStr), abb.MaxRequirementsCharsForLLMCheck)
			logger.Warn(warnMsg)
			writeBadRequestError(w, fmt.Errorf("%s", warnMsg))
			return
		}
		// --- End Requirements Length Check ---

		// --- Infer PlatformKind and ContentKind using LLM ---
		validPlatformKinds := []abb.PlatformKind{
			abb.PlatformReddit,
			abb.PlatformYouTube,
			abb.PlatformTwitch,
			abb.PlatformHackerNews,
			abb.PlatformBluesky,
			abb.PlatformInstagram,
			abb.PlatformIncentivizeThis,
			abb.PlatformTripAdvisor,
			abb.PlatformSteam,
			abb.PlatformGitHub,
		}
		validContentKinds := []abb.ContentKind{
			abb.ContentKindPost,
			abb.ContentKindComment,
			abb.ContentKindVideo,
			abb.ContentKindClip,
			abb.ContentKindBounty,
			abb.ContentKindReview,
			abb.ContentKindDota2Chat,
			abb.ContentKindIssue,
		}

		// Convert valid kinds to string slices for the prompt
		validPlatformKindsStr := make([]string, len(validPlatformKinds))
		for i, pk := range validPlatformKinds {
			validPlatformKindsStr[i] = string(pk)
		}
		validContentKindsStr := make([]string, len(validContentKinds))
		for i, ck := range validContentKinds {
			validContentKindsStr[i] = string(ck)
		}

		// Define the JSON schema for the expected output
		schema := map[string]interface{}{
			"name":   "infer_content_parameters",
			"strict": true,
			"schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"PlatformKind": map[string]interface{}{
						"type":        "string",
						"description": "The platform for the content (e.g., 'reddit', 'youtube').",
						"enum":        validPlatformKindsStr,
					},
					"ContentKind": map[string]interface{}{
						"type": "string",
						"description": `The kind of content that the bounty is for. This is platform dependent. The valid options are:
- Reddit: post, comment
- YouTube: video
- Twitch: video, clip
- Hacker News: post, comment
- Bluesky: post
- Instagram: post
- IncentivizeThis: bounty
- TripAdvisor: review
- Steam: dota2chat
- GitHub: issue`,
						"enum": validContentKindsStr,
					},
					"Error": map[string]interface{}{
						"type":        "string",
						"description": "Set only if determination is not possible.",
					},
				},
				// these are needed because we have "strict" set to true
				"required":             []string{"PlatformKind", "ContentKind", "Error"},
				"additionalProperties": false,
			},
		}
		schemaJSON, err := json.Marshal(schema)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to marshal content parameters schema: %w", err))
			return
		}
		resp, err := llmProvider.GenerateResponse(r.Context(), prompts.InferContentParams, requirementsStr, schemaJSON)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to infer content parameters: %w", err))
			return
		}
		var inferredParams inferredContentParamsRequest
		if err := json.Unmarshal([]byte(resp), &inferredParams); err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to parse inferred content parameters: %w", err))
			return
		}

		if inferredParams.Error != "" {
			writeBadRequestError(w, fmt.Errorf("failed to infer content parameters: %s", inferredParams.Error))
			return
		}

		// Validate inferred PlatformKind
		normalizedPlatform := abb.PlatformKind(strings.ToLower(inferredParams.PlatformKind))
		validPlatformFound := false
		for _, vp := range validPlatformKinds {
			if normalizedPlatform == vp {
				validPlatformFound = true
				break
			}
		}
		if !validPlatformFound {
			errMsg := fmt.Sprintf("LLM inferred an unsupported PlatformKind: '%s'. Supported kinds are: %s", inferredParams.PlatformKind, strings.Join(validPlatformKindsStr, ", "))
			logger.Warn(errMsg)
			writeBadRequestError(w, fmt.Errorf("%s", errMsg))
			return
		}

		// Validate inferred ContentKind
		normalizedContentKind := abb.ContentKind(strings.ToLower(inferredParams.ContentKind))
		validContentFound := false
		for _, vc := range validContentKinds {
			if normalizedContentKind == vc {
				validContentFound = true
				break
			}
		}
		if !validContentFound {
			errMsg := fmt.Sprintf("LLM inferred an unsupported ContentKind: '%s'. Supported kinds are: %s", inferredParams.ContentKind, strings.Join(validContentKindsStr, ", "))
			logger.Warn(errMsg)
			writeBadRequestError(w, fmt.Errorf("%s", errMsg))
			return
		}

		// Validate platform type and content kind using normalized values
		switch normalizedPlatform {
		case abb.PlatformReddit:
			if normalizedContentKind != abb.ContentKindPost && normalizedContentKind != abb.ContentKindComment {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Reddit: must be '%s' or '%s'", abb.ContentKindPost, abb.ContentKindComment))
				return
			}
		case abb.PlatformYouTube:
			if normalizedContentKind != abb.ContentKindVideo && normalizedContentKind != abb.ContentKindComment {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for YouTube: must be '%s' or '%s'", abb.ContentKindVideo, abb.ContentKindComment))
				return
			}
		case abb.PlatformTwitch:
			if normalizedContentKind != abb.ContentKindVideo && normalizedContentKind != abb.ContentKindClip {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Twitch: must be '%s' or '%s'", abb.ContentKindVideo, abb.ContentKindClip))
				return
			}
		case abb.PlatformHackerNews:
			if normalizedContentKind != abb.ContentKindPost && normalizedContentKind != abb.ContentKindComment {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Hacker News: must be '%s' or '%s'", abb.ContentKindPost, abb.ContentKindComment))
				return
			}
		case abb.PlatformBluesky:
			if normalizedContentKind != abb.ContentKindPost { // Assuming only posts for Bluesky initially
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Bluesky: must be '%s'", abb.ContentKindPost))
				return
			}
		case abb.PlatformInstagram:
			if normalizedContentKind != abb.ContentKindPost {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Instagram: must be '%s' (not '%s')", abb.ContentKindPost, normalizedContentKind))
				return
			}
		case abb.PlatformIncentivizeThis:
			if normalizedContentKind != abb.ContentKindBounty {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for IncentivizeThis: must be '%s'", abb.ContentKindBounty))
				return
			}
		case abb.PlatformTripAdvisor:
			if normalizedContentKind != abb.ContentKindReview {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for TripAdvisor: must be '%s'", abb.ContentKindReview))
				return
			}
		case abb.PlatformSteam:
			if normalizedContentKind != abb.ContentKindDota2Chat {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Steam: must be '%s'", abb.ContentKindDota2Chat))
				return
			}
		case abb.PlatformGitHub:
			if normalizedContentKind != abb.ContentKindIssue {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for GitHub: must be '%s'", abb.ContentKindIssue))
				return
			}
		default:
			writeBadRequestError(w, fmt.Errorf("invalid platform_kind: must be one of %s, %s, %s, %s, %s, %s, %s, %s, %s, or %s", abb.PlatformReddit, abb.PlatformYouTube, abb.PlatformTwitch, abb.PlatformHackerNews, abb.PlatformBluesky, abb.PlatformInstagram, abb.PlatformIncentivizeThis, abb.PlatformTripAdvisor, abb.PlatformSteam, abb.PlatformGitHub))
			return
		}

		// Read and validate overall bounty timeout
		bountyTimeoutDuration := 7 * 24 * time.Hour
		if req.TimeoutDuration != "" {
			parsedDurationString := req.TimeoutDuration
			if strings.HasSuffix(strings.ToLower(req.TimeoutDuration), "d") {
				daysStr := strings.TrimSuffix(strings.ToLower(req.TimeoutDuration), "d")
				days, err := strconv.Atoi(daysStr)
				if err != nil {
					writeBadRequestError(w, fmt.Errorf("invalid day value in timeout_duration '%s': %w", req.TimeoutDuration, err))
					return
				}
				if days <= 0 {
					writeBadRequestError(w, fmt.Errorf("day value in timeout_duration '%s' must be positive", req.TimeoutDuration))
					return
				}
				hours := days * 24
				parsedDurationString = fmt.Sprintf("%dh", hours)
				logger.Info("Converted day-based duration to hours", "original_duration", req.TimeoutDuration, "converted_duration", parsedDurationString)
			}

			duration, err := time.ParseDuration(parsedDurationString)
			if err != nil {
				writeBadRequestError(w, fmt.Errorf("invalid timeout_duration format '%s' (parsed as '%s'): %w", req.TimeoutDuration, parsedDurationString, err))
				return
			}
			bountyTimeoutDuration = duration
		}

		// Validate minimum duration
		if bountyTimeoutDuration < 24*time.Hour {
			writeBadRequestError(w, fmt.Errorf("timeout_duration must be at least 24 hours (e.g., \"24h\")"))
			return
		}

		// Apply revenue sharing using the calculator function
		userBountyPerPost := req.BountyPerPost
		var userTotalBounty float64

		// Determine fee percentage to use
		var feePercentage float64
		claims, ok := r.Context().Value(ctxKeyJWT).(*authJWTClaims)
		if ok && claims.Status >= UserStatusSudo && req.FeePercentage != nil && *req.FeePercentage >= 0 {
			feePercentage = *req.FeePercentage
		} else {
			feePercentage = defaultFeePercentage
		}

		// Max Payouts Per User
		maxPayoutsPerUser := 1
		if claims.Status >= UserStatusSudo && req.MaxPayoutsPerUser != nil && *req.MaxPayoutsPerUser > 0 && *req.MaxPayoutsPerUser <= 100 {
			maxPayoutsPerUser = *req.MaxPayoutsPerUser
		}

		// Calculate the final bounty amount for the user after platform fees.
		// This uses go-money for safe currency allocation.
		totalMoney := money.NewFromFloat(req.TotalBounty, money.USD)
		feeIntPercentage := int(feePercentage)
		userIntPercentage := 100 - feeIntPercentage

		parties, err := totalMoney.Allocate(userIntPercentage, feeIntPercentage)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to allocate bounty amount: %w", err))
			return
		}
		userTotalBounty = parties[0].AsMajorUnits()

		// --- START: Validate BountyPerPost against effective TotalBounty ---
		if userBountyPerPost > userTotalBounty {
			errMsg := fmt.Sprintf("bounty_per_post (%.6f) cannot be greater than the effective total_bounty after fees (%.6f)", userBountyPerPost, userTotalBounty)
			logger.Warn(errMsg, "raw_bounty_per_post", req.BountyPerPost, "raw_total_bounty", req.TotalBounty, "effective_total_bounty", userTotalBounty)
			writeBadRequestError(w, fmt.Errorf("%s", errMsg))
			return
		}
		// --- END: Validate BountyPerPost against effective TotalBounty ---

		// Convert amounts to USDCAmount
		bountyPerPost, err := solana.NewUSDCAmount(userBountyPerPost)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid bounty_per_post amount: %w", err))
			return
		}
		totalBounty, err := solana.NewUSDCAmount(userTotalBounty)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid total_bounty amount: %w", err))
			return
		}

		// Convert ORIGINAL total bounty for verification
		totalCharged, err := solana.NewUSDCAmount(req.TotalBounty)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid original total_bounty amount: %w", err))
			return
		}

		// Create workflow input
		input := abb.BountyAssessmentWorkflowInput{
			Title:             bountyTitle,
			Requirements:      req.Requirements,
			BountyPerPost:     bountyPerPost,
			TotalBounty:       totalBounty,
			TotalCharged:      totalCharged,
			Platform:          normalizedPlatform,
			ContentKind:       normalizedContentKind,
			Tier:              bountyTier,
			Timeout:           bountyTimeoutDuration,
			PaymentTimeout:    10 * time.Minute,
			TreasuryWallet:    os.Getenv(EnvSolanaTreasuryWallet),
			EscrowWallet:      os.Getenv(EnvSolanaEscrowWallet),
			MaxPayoutsPerUser: maxPayoutsPerUser,
		}

		// Execute workflow
		workflowID := fmt.Sprintf("bounty-%s", uuid.New().String())
		now := time.Now().UTC()

		// Create typed search attributes using defined keys
		saMap := temporal.NewSearchAttributes(
			abb.EnvironmentKey.ValueSet(env),
			abb.BountyPlatformKey.ValueSet(string(input.Platform)),
			abb.BountyContentKindKey.ValueSet(string(input.ContentKind)),
			abb.BountyTierKey.ValueSet(int64(input.Tier)),
			abb.BountyTotalAmountKey.ValueSet(input.TotalBounty.ToUSDC()),
			abb.BountyPerPostAmountKey.ValueSet(input.BountyPerPost.ToUSDC()),
			abb.BountyCreationTimeKey.ValueSet(now),
			abb.BountyTimeoutTimeKey.ValueSet(now.Add(input.Timeout)),
			abb.BountyStatusKey.ValueSet(string(abb.BountyStatusAwaitingFunding)),
			abb.BountyValueRemainingKey.ValueSet(input.TotalBounty.ToUSDC()),
		)

		workflowOptions := client.StartWorkflowOptions{
			ID:                    workflowID,
			TaskQueue:             os.Getenv(EnvTaskQueue),
			TypedSearchAttributes: saMap,
		}

		_, err = tc.ExecuteWorkflow(r.Context(), workflowOptions, abb.BountyAssessmentWorkflow, input)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to start workflow: %w", err))
			return
		}

		writeJSONResponse(w, api.CreateBountySuccessResponse{
			Message:                 "Bounty creation initiated and workflow started.",
			BountyID:                workflowID,
			TotalCharged:            input.TotalCharged.ToUSDC(),
			PaymentTimeoutExpiresAt: now.Add(input.PaymentTimeout),
		}, http.StatusOK)
	}
}

// handleHardenBounty uses the LLM to harden user-provided bounty requirements.
// It enforces a current-date requirement and discourages astroturfing/inauthentic content.
func handleHardenBounty(
	logger *slog.Logger,
	llmProvider abb.LLMProvider,
	cache *expirable.LRU[string, any],
	prompts struct {
		HardenBounty string
	},
) http.HandlerFunc {
	type hardenRequest struct {
		Requirements string `json:"requirements"`
	}
	type hardenResponse struct {
		Requirements string `json:"requirements"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req hardenRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}
		if strings.TrimSpace(req.Requirements) == "" {
			writeBadRequestError(w, fmt.Errorf("requirements is required"))
			return
		}

		// Check cache first
		if cached, ok := cache.Get(req.Requirements); ok {
			writeJSONResponse(w, cached, http.StatusOK)
			return
		}

		// Prepare prompt with current date context
		// Enforce structured output for hardened requirements
		hardenSchema := map[string]interface{}{
			"name":   "harden_bounty",
			"strict": true,
			"schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"requirements": map[string]interface{}{
						"type":        "string",
						"description": "Final hardened bounty requirements as a single string (newline-separated if needed).",
					},
				},
				"required":             []string{"requirements"},
				"additionalProperties": false,
			},
		}
		hardenSchemaJSON, err := json.Marshal(hardenSchema)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to marshal harden schema: %w", err))
			return
		}
		resp, err := llmProvider.GenerateResponse(r.Context(), prompts.HardenBounty, req.Requirements, hardenSchemaJSON)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to harden bounty: %w", err))
			return
		}
		var out hardenResponse
		if err := json.Unmarshal([]byte(resp), &out); err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to parse harden response: %w", err))
			return
		}
		if strings.TrimSpace(out.Requirements) == "" {
			writeBadRequestError(w, fmt.Errorf("LLM returned empty hardened requirements"))
			return
		}

		// Add to cache
		cache.Add(req.Requirements, out)

		writeJSONResponse(w, out, http.StatusOK)
	}
}

// handleListBounties handles listing all bounties
func handleListBounties(l *slog.Logger, tc client.Client, env string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// List workflows of type BountyAssessmentWorkflow
		// Use a background context for listing, as it might take time
		listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second) // Timeout for the whole list operation
		defer listCancel()

		// --- Tier-based Filtering ---
		userTier, ok := r.Context().Value(ctxKeyTier).(int)
		if !ok {
			// If tier is not in context (e.g., unauthenticated user), default to the highest tier (most public)
			userTier = int(abb.BountyTierAltruist)
		}

		// --- Build Query ---
		queryParts := []string{
			fmt.Sprintf("WorkflowType = '%s'", "BountyAssessmentWorkflow"),
			"ExecutionStatus = 'Running'",
			fmt.Sprintf("%s = '%s'", abb.EnvironmentKey.GetName(), env),
			fmt.Sprintf("%s >= %d", abb.BountyTierKey.GetName(), userTier),
		}

		funderWallet := r.URL.Query().Get("funder_wallet")
		if funderWallet != "" {
			// Validate that funderWallet is a valid Solana address
			if _, err := solanago.PublicKeyFromBase58(funderWallet); err != nil {
				writeBadRequestError(w, fmt.Errorf("invalid funder_wallet address '%s': %w", funderWallet, err))
				return
			}
			queryParts = append(queryParts, fmt.Sprintf("%s = '%s'", abb.BountyFunderWalletKey.GetName(), funderWallet))
		}

		query := strings.Join(queryParts, " AND ")
		executions, err := tc.ListWorkflow(listCtx, &workflowservice.ListWorkflowExecutionsRequest{
			Query: query,
		})
		if err != nil {
			if listCtx.Err() == context.DeadlineExceeded {
				writeInternalError(l, w, fmt.Errorf("timed out listing bounties: %w", err))
			} else {
				writeInternalError(l, w, fmt.Errorf("failed to list bounties: %w", err))
			}
			return
		}

		// Convert workflows to bounty list items
		bounties := make([]api.BountyListItem, 0)
		for _, execution := range executions.Executions {
			// Get workflow input from history
			var input abb.BountyAssessmentWorkflowInput

			// Use a short timeout for fetching history for *each* workflow
			histCtx, histCancel := context.WithTimeout(context.Background(), 5*time.Second)

			historyIterator := tc.GetWorkflowHistory(histCtx, execution.Execution.WorkflowId, execution.Execution.RunId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			if !historyIterator.HasNext() {
				l.Error("failed to get workflow history or history is empty", "workflow_id", execution.Execution.WorkflowId)
				histCancel() // Ensure context is cancelled
				continue
			}

			event, err := historyIterator.Next()
			if err != nil {
				l.Error("failed to get first history event", "error", err, "workflow_id", execution.Execution.WorkflowId)
				histCancel()
				continue
			}

			if event.GetEventType() != enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
				l.Error("first history event is not WorkflowExecutionStarted", "event_type", event.GetEventType(), "workflow_id", execution.Execution.WorkflowId)
				histCancel()
				continue
			}

			attrs := event.GetWorkflowExecutionStartedEventAttributes()
			if attrs == nil || attrs.Input == nil || len(attrs.Input.Payloads) == 0 {
				l.Error("WorkflowExecutionStarted event missing input attributes", "workflow_id", execution.Execution.WorkflowId)
				histCancel()
				continue
			}

			// Decode the input payload
			// Assuming input is the first payload
			err = converter.GetDefaultDataConverter().FromPayload(attrs.Input.Payloads[0], &input)
			if err != nil {
				l.Error("failed to decode workflow input from history", "error", err, "workflow_id", execution.Execution.WorkflowId)
				histCancel()
				continue
			}

			histCancel() // Cancel context as soon as history is fetched

			// Extract EndTime from Search Attributes (from the ListWorkflow response)
			var endTime time.Time
			var status string
			var tier int64 // Use int64 to match the search attribute type

			if execution.SearchAttributes != nil {
				if saPayload, ok := execution.SearchAttributes.GetIndexedFields()[abb.BountyTimeoutTimeKey.GetName()]; ok {
					err = converter.GetDefaultDataConverter().FromPayload(saPayload, &endTime)
					if err != nil {
						l.Warn("Failed to decode BountyTimeoutTime search attribute for list item", "workflow_id", execution.Execution.WorkflowId, "error", err)
						endTime = time.Time{} // Set zero value on error
					}
				} else {
					l.Warn("BountyTimeoutTime search attribute not found for list item", "workflow_id", execution.Execution.WorkflowId)
					endTime = time.Time{} // Set zero value if missing
				}

				// Get BountyStatus from Search Attributes
				if statusPayload, ok := execution.SearchAttributes.GetIndexedFields()[abb.BountyStatusKey.GetName()]; ok {
					var decodedStatus string
					err = converter.GetDefaultDataConverter().FromPayload(statusPayload, &decodedStatus)
					if err != nil {
						l.Warn("Failed to decode BountyStatus search attribute for list item", "workflow_id", execution.Execution.WorkflowId, "error", err)
						status = execution.Status.String() // Fallback to Temporal status
					} else {
						status = decodedStatus
					}
				} else {
					l.Warn("BountyStatus search attribute not found for list item", "workflow_id", execution.Execution.WorkflowId)
					status = execution.Status.String() // Fallback to Temporal status
				}

				// Get BountyTier from Search Attributes
				if tierPayload, ok := execution.SearchAttributes.GetIndexedFields()[abb.BountyTierKey.GetName()]; ok {
					err = converter.GetDefaultDataConverter().FromPayload(tierPayload, &tier)
					if err != nil {
						l.Warn("Failed to decode BountyTier search attribute for list item", "workflow_id", execution.Execution.WorkflowId, "error", err)
						tier = int64(abb.DefaultBountyTier) // Fallback to default
					}
				} else {
					l.Warn("BountyTier search attribute not found for list item", "workflow_id", execution.Execution.WorkflowId)
					tier = int64(abb.DefaultBountyTier) // Fallback to default
				}
			} else {
				l.Warn("SearchAttributes missing in ListWorkflow response item", "workflow_id", execution.Execution.WorkflowId)
				endTime = time.Time{}
				status = execution.Status.String()
				tier = int64(abb.DefaultBountyTier)
			}

			var remainingBountyValue float64
			if val, ok := execution.SearchAttributes.GetIndexedFields()[abb.BountyValueRemainingKey.GetName()]; ok {
				err = converter.GetDefaultDataConverter().FromPayload(val, &remainingBountyValue)
				if err != nil {
					l.Error("Failed to decode BountyValueRemainingKey", "error", err, "workflow_id", execution.Execution.WorkflowId)
					remainingBountyValue = 0.0 // Default to 0 on error
				}
			} else {
				l.Warn("BountyValueRemainingKey not found", "workflow_id", execution.Execution.WorkflowId)
				// Default remaining to total if not found (might happen for older workflows or before initial set)
				remainingBountyValue = input.TotalBounty.ToUSDC()
			}

			var funderWallet string
			if saPayload, ok := execution.SearchAttributes.GetIndexedFields()[abb.BountyFunderWalletKey.GetName()]; ok {
				err = converter.GetDefaultDataConverter().FromPayload(saPayload, &funderWallet)
				if err != nil {
					l.Warn("Failed to decode BountyFunderWallet search attribute for list item", "workflow_id", execution.Execution.WorkflowId, "error", err)
				}
			} else {
				l.Warn("BountyFunderWallet search attribute not found for list item", "workflow_id", execution.Execution.WorkflowId)
			}

			bounty := api.BountyListItem{
				BountyID:             execution.Execution.WorkflowId,
				Title:                input.Title,
				Status:               status,
				Requirements:         input.Requirements,
				BountyPerPost:        input.BountyPerPost.ToUSDC(),
				TotalBounty:          input.TotalBounty.ToUSDC(),
				TotalCharged:         input.TotalCharged.ToUSDC(),
				RemainingBountyValue: remainingBountyValue,
				BountyFunderWallet:   funderWallet,
				PlatformKind:         string(input.Platform),
				ContentKind:          string(input.ContentKind),
				Tier:                 int(tier),
				CreatedAt:            execution.StartTime.AsTime(),
				EndAt:                endTime,
			}

			if status == string(abb.BountyStatusAwaitingFunding) {
				expiresAt := execution.StartTime.AsTime().Add(input.PaymentTimeout)
				bounty.PaymentTimeoutExpiresAt = &expiresAt
			}

			bounties = append(bounties, bounty)
		}

		writeJSONResponse(w, bounties, http.StatusOK)
	}
}

// handleAssessContent handles assessing content against requirements
func handleAssessContent(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req AssessContentRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}

		if req.BountyID == "" || req.ContentID == "" || req.PayoutWallet == "" || req.Platform == "" {
			writeBadRequestError(w, fmt.Errorf("invalid request: missing required fields: bounty_id, content_id, payout_wallet, platform: %v", req))
			return
		}

		// Validate that PayoutWallet is a valid Solana address
		if _, err := solanago.PublicKeyFromBase58(req.PayoutWallet); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid payout_wallet address '%s': %w", req.PayoutWallet, err))
			return
		}

		// Normalize platform and content kind to lowercase for consistent handling
		normalizedPlatform := abb.PlatformKind(strings.ToLower(string(req.Platform)))
		normalizedContentKind := abb.ContentKind(strings.ToLower(string(req.ContentKind)))

		// Signal the workflow
		err := tc.SignalWorkflow(r.Context(), req.BountyID, "", abb.AssessmentSignalName, abb.AssessContentSignal{
			ContentID:    req.ContentID,
			PayoutWallet: req.PayoutWallet,
			Platform:     normalizedPlatform,    // Use normalized value
			ContentKind:  normalizedContentKind, // Use normalized value
		})
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to signal workflow: %w", err))
			return
		}

		// Return success response
		writeJSONResponse(w, DefaultJSONResponse{
			Message: "Content assessment initiated",
		}, http.StatusOK)
	}
}

// handleListPaidBounties handles listing all paid bounties from the database
func handleListPaidBounties(l *slog.Logger, querier dbgen.Querier, escrowWallet string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get limit from query parameter, default to 100
		limit := int32(100)
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if parsedLimit, err := strconv.ParseInt(limitStr, 10, 32); err == nil && parsedLimit > 0 {
				limit = int32(parsedLimit)
			}
		}

		// Get outgoing transactions from database
		transactions, err := querier.GetOutgoingSolanaTransactions(r.Context(), dbgen.GetOutgoingSolanaTransactionsParams{
			FunderWallet: escrowWallet,
			Limit:        limit,
		})
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to get outgoing transactions: %w", err))
			return
		}

		// Get treasury wallet for filtering
		treasuryWalletAddress := os.Getenv(EnvSolanaTreasuryWallet)

		// Filter and convert transactions to PaidBountyItem format
		paidBounties := make([]PaidBountyItem, 0)
		for _, tx := range transactions {
			// Skip transactions to treasury wallet
			if treasuryWalletAddress != "" && tx.RecipientWallet == treasuryWalletAddress {
				continue
			}

			// Check if this is a bounty payout (has bounty memo)
			isBountyPayout := false
			if tx.Memo.Valid && tx.Memo.String != "" {
				var memoData map[string]interface{}
				if err := json.Unmarshal([]byte(tx.Memo.String), &memoData); err == nil {
					_, hasWorkflowID := memoData["bounty_id"]
					_, hasContentID := memoData["content_id"]
					isBountyPayout = hasWorkflowID && hasContentID
				}
			}

			if !isBountyPayout {
				continue
			}

			// Convert amount from smallest unit to USDC
			amountUSDC := float64(tx.AmountSmallestUnit) / math.Pow10(6)

			paidBounty := PaidBountyItem{
				Signature:            tx.Signature,
				Timestamp:            tx.BlockTime.Time.UTC(),
				RecipientOwnerWallet: tx.RecipientWallet,
				Amount:               amountUSDC,
			}
			if tx.Memo.Valid {
				paidBounty.Memo = tx.Memo.String
			}
			paidBounties = append(paidBounties, paidBounty)
		}

		// Apply final limit for response
		recentBountyCountLimit := 10
		if r.URL.Query().Get("limit") != "" {
			if parsedLimit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsedLimit > 0 {
				recentBountyCountLimit = parsedLimit
			}
		}
		if len(paidBounties) > recentBountyCountLimit {
			paidBounties = paidBounties[:recentBountyCountLimit]
		}

		l.Debug("Serving paid bounties from database", "item_count", len(paidBounties))
		writeJSONResponse(w, paidBounties, http.StatusOK)
	}
}

// handleListPaidBountiesForWorkflow handles listing paid bounties for a specific workflow via a query.
func handleListPaidBountiesForWorkflow(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("bounty_id")
		if workflowID == "" {
			writeBadRequestError(w, fmt.Errorf("missing bounty_id in path"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second) // Use request context with timeout
		defer cancel()

		resp, err := tc.QueryWorkflow(ctx, workflowID, "", abb.GetPaidBountiesQueryType)
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			var queryFailedErr *serviceerror.QueryFailed // Specific error for query failures

			if errors.As(err, &notFoundErr) {
				writeNotFoundError(w)
			} else if errors.As(err, &queryFailedErr) {
				// This can happen if the query handler isn't set up, or panics.
				writeInternalError(l, w, fmt.Errorf("failed to query workflow %s: %w", workflowID, err))
			} else {
				writeInternalError(l, w, fmt.Errorf("error querying workflow %s: %w", workflowID, err))
			}
			return
		}

		var paidBounties []abb.PayoutDetail
		if err := resp.Get(&paidBounties); err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to decode query result for workflow %s: %w", workflowID, err))
			return
		}

		if paidBounties == nil { // if query returns nil, make it an empty slice for JSON response
			paidBounties = make([]abb.PayoutDetail, 0)
		}

		// iterate over paidBounties and strip out content we don't want to expose
		// to the client, like the content_id and payout_wallet
		for i, _ := range paidBounties {
			paidBounties[i].ContentID = ""
			paidBounties[i].PayoutWallet = ""
		}

		writeJSONResponse(w, paidBounties, http.StatusOK)
	}
}

// handleGetBountyByID handles fetching details for a single bounty by its ID
func handleGetBountyByID(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id") // Extract ID from path e.g., /bounties/{id}
		if workflowID == "" {
			writeBadRequestError(w, fmt.Errorf("missing bounty ID in path"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		resp, err := tc.QueryWorkflow(ctx, workflowID, "", abb.GetBountyDetailsQueryType)
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			if errors.As(err, &notFoundErr) {
				writeEmptyResultError(w)
			} else {
				writeInternalError(l, w, fmt.Errorf("failed to query workflow %s for details: %w", workflowID, err))
			}
			return
		}

		var bountyDetails abb.BountyDetails
		if err := resp.Get(&bountyDetails); err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to decode bounty details from query for workflow %s: %w", workflowID, err))
			return
		}

		// Convert abb.BountyDetails to api.BountyListItem
		// This is a bit redundant but keeps the API response consistent.
		// We could consider changing the API to return BountyDetails directly in the future.
		bountyListItem := api.BountyListItem{
			BountyID:             bountyDetails.BountyID,
			Title:                bountyDetails.Title,
			Status:               string(bountyDetails.Status),
			Requirements:         bountyDetails.Requirements,
			BountyPerPost:        bountyDetails.BountyPerPost,
			TotalBounty:          bountyDetails.TotalBounty,
			TotalCharged:         bountyDetails.TotalCharged,
			RemainingBountyValue: bountyDetails.RemainingBountyValue,
			BountyFunderWallet:   bountyDetails.BountyFunderWallet,
			PlatformKind:         string(bountyDetails.PlatformKind),
			ContentKind:          string(bountyDetails.ContentKind),
			Tier:                 int(bountyDetails.Tier),
			CreatedAt:            bountyDetails.CreatedAt,
			EndAt:                bountyDetails.EndAt,
		}
		if bountyDetails.PaymentTimeoutExpiresAt != nil {
			bountyListItem.PaymentTimeoutExpiresAt = bountyDetails.PaymentTimeoutExpiresAt
		}

		writeJSONResponse(w, bountyListItem, http.StatusOK)
	}
}

// handleStoreBountySummary handles storing the final summary of a bounty.
// This is intended to be called by a Temporal activity at the end of a bounty workflow.
func handleStoreBountySummary(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var summaryData abb.BountySummaryData
		if err := stools.DecodeJSONBody(r, &summaryData); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request body for bounty summary: %w", err))
			return
		}

		if summaryData.BountyID == "" {
			writeBadRequestError(w, fmt.Errorf("bounty_id is required in summary data"))
			return
		}

		// The summaryData is already in the correct struct form (abb.BountySummaryData)
		// We need to marshal it to json.RawMessage for sqlc, as the db column is JSONB.
		summaryJSON, err := json.Marshal(summaryData)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to marshal summary data to JSON for DB: %w", err))
			return
		}

		params := dbgen.UpsertBountySummaryParams{
			BountyID: summaryData.BountyID,
			Summary:  summaryJSON, // Pass the json.RawMessage
		}

		if err := querier.UpsertBountySummary(r.Context(), params); err != nil {
			// Check for specific DB errors if needed, e.g., constraint violations
			writeInternalError(logger, w, fmt.Errorf("failed to store bounty summary in DB: %w", err))
			return
		}

		logger.Info("Successfully stored bounty summary", "bounty_id", summaryData.BountyID, "final_status", summaryData.FinalStatus)
		writeJSONResponse(w, DefaultJSONResponse{Message: "Bounty summary stored successfully"}, http.StatusCreated)
	}
}

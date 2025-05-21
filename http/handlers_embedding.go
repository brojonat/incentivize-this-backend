package http

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"github.com/pgvector/pgvector-go"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

const (
	defaultSearchLimit = 20
)

// StoreEmbeddingRequest is the request body for storing a bounty embedding.
type StoreEmbeddingRequest struct {
	BountyID  string          `json:"bounty_id"`
	Embedding pgvector.Vector `json:"embedding"` // Using pgvector.Vector type
}

// handleStoreBountyEmbedding handles storing a bounty embedding in the database.
func handleStoreBountyEmbedding(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req StoreEmbeddingRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}

		if req.BountyID == "" || len(req.Embedding.Slice()) == 0 {
			writeBadRequestError(w, errors.New("bounty_id and embedding are required"))
			return
		}

		params := dbgen.InsertEmbeddingParams{
			BountyID:  req.BountyID,
			Embedding: req.Embedding,
		}

		if err := querier.InsertEmbedding(r.Context(), params); err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to store embedding: %w", err))
			return
		}

		writeOK(w)
	}
}

// handleSearchBounties handles searching for bounties using text embeddings.
func handleSearchBounties(logger *slog.Logger, querier dbgen.Querier, tc client.Client, llmProvider abb.LLMEmbeddingProvider, env string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queryText := r.URL.Query().Get("q")
		if queryText == "" {
			writeBadRequestError(w, errors.New("search query 'q' is required"))
			return
		}

		if llmProvider == nil {
			writeInternalError(logger, w, errors.New("search functionality disabled: LLM embedding provider not configured"))
			return
		}

		// This model name should ideally come from a shared config or be consistent
		// For now, using a placeholder. It must match the model used for storing embeddings.
		// Ideally, the activity gets this from abb.Configuration which is read from env.
		// The HTTP server could also get this from an env var for the LLMEmbeddingProvider initialization.
		// For now, let's assume there is a configured model name available to the provider implicitly or via its init.
		// If modelName is part of abb.Configuration.ABBServerConfig.LLMEmbeddingModel, it should be passed to the provider.
		// Let's assume the llmProvider is configured with the correct model.
		embeddingSlice, err := llmProvider.GenerateEmbedding(r.Context(), queryText, "") // Passing empty model, assuming provider knows.
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to generate query embedding: %w", err))
			return
		}
		embeddingVec := pgvector.NewVector(embeddingSlice)

		limit := defaultSearchLimit // Add ability to parse limit from query params if needed

		searchParams := dbgen.SearchEmbeddingsParams{
			Embedding: embeddingVec,
			Limit:     int32(limit),
		}

		results, err := querier.SearchEmbeddings(r.Context(), searchParams)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to search embeddings: %w", err))
			return
		}

		if len(results) == 0 {
			writeJSONResponse(w, []BountyListItem{}, http.StatusOK) // Return empty list, not an error
			return
		}

		// Fetch full bounty details for the found IDs
		bountyIDs := make([]string, len(results))
		for i, res := range results {
			bountyIDs[i] = res.BountyID
		}

		// This part is complex as it requires fetching and reconstructing BountyListItems
		// from multiple workflow executions. Similar logic to handleListBounties but filtered by IDs.
		// For simplicity in this step, I will return the IDs and a note.
		// In a full implementation, you would iterate `bountyIDs`, describe each workflow,
		// get its input and search attributes to build `BountyListItem` objects.

		logger.Info("Search successful", "query", queryText, "found_ids", bountyIDs)

		// Placeholder: Fetch full bounty details based on bountyIDs
		// This would involve iterating through bountyIDs, calling tc.DescribeWorkflowExecution for each,
		// then tc.GetWorkflowHistory to get the input, and then constructing BountyListItem.
		// This is non-trivial. For now, just returning the IDs for brevity.
		// A simpler start might be to return just the bounty_ids, or implement a simplified fetch.

		// Let's try to fetch details for found bounties, similar to handleListBounties but more targeted.
		// This is a simplified version and might be slow if many IDs are returned.
		// Proper batching or a dedicated Temporal query for batch fetching details would be better.
		var detailedBounties []BountyListItem
		for _, id := range bountyIDs {
			// This is a simplified version of fetching bounty details. Refer to handleGetBountyByID or handleListBounties
			// for more complete logic to extract all fields for BountyListItem.
			// We need to get execution info (status, start time) and input (requirements, amounts etc.)
			descResp, err := tc.DescribeWorkflowExecution(r.Context(), id, "")
			if err != nil {
				logger.Warn("Failed to describe workflow for search result", "workflow_id", id, "error", err)
				continue
			}
			if descResp == nil || descResp.WorkflowExecutionInfo == nil || descResp.WorkflowExecutionInfo.Execution == nil {
				logger.Warn("Invalid description for workflow search result", "workflow_id", id)
				continue
			}

			var input abb.BountyAssessmentWorkflowInput
			historyIterator := tc.GetWorkflowHistory(r.Context(), id, descResp.WorkflowExecutionInfo.Execution.RunId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			if historyIterator.HasNext() {
				event, err := historyIterator.Next()
				if err == nil && event.GetEventType() == enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
					attrs := event.GetWorkflowExecutionStartedEventAttributes()
					if attrs != nil && attrs.Input != nil && len(attrs.Input.Payloads) > 0 {
						_ = converter.GetDefaultDataConverter().FromPayload(attrs.Input.Payloads[0], &input)
					}
				}
			}

			var endTime time.Time
			if saPayload, ok := descResp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[abb.BountyTimeoutTimeKey.GetName()]; ok {
				_ = converter.GetDefaultDataConverter().FromPayload(saPayload, &endTime)
			}
			var remainingBountyValue float64
			if val, ok := descResp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[abb.BountyValueRemainingKey.GetName()]; ok {
				_ = converter.GetDefaultDataConverter().FromPayload(val, &remainingBountyValue)
			} else {
				remainingBountyValue = input.TotalBounty.ToUSDC() // Default if not found
			}

			bountyItem := BountyListItem{
				WorkflowID:           id,
				Status:               descResp.WorkflowExecutionInfo.Status.String(),
				Requirements:         input.Requirements, // May be empty if history fetch failed
				BountyPerPost:        input.BountyPerPost.ToUSDC(),
				TotalBounty:          input.TotalBounty.ToUSDC(),
				RemainingBountyValue: remainingBountyValue,
				BountyOwnerWallet:    input.BountyOwnerWallet,
				PlatformType:         input.Platform,
				ContentKind:          input.ContentKind,
				CreatedAt:            descResp.WorkflowExecutionInfo.StartTime.AsTime(),
				EndTime:              endTime,
			}
			detailedBounties = append(detailedBounties, bountyItem)
		}

		writeJSONResponse(w, detailedBounties, http.StatusOK)
	}
}

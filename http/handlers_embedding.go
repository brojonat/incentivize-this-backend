package http

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"github.com/pgvector/pgvector-go"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

const (
	defaultSearchLimit = 10
)

// StoreEmbeddingRequest is the request body for storing a bounty embedding.
type StoreEmbeddingRequest struct {
	BountyID    string          `json:"bounty_id"`
	Embedding   pgvector.Vector `json:"embedding"`
	Environment string          `json:"environment"`
}

// handleStoreBountyEmbedding handles storing a bounty embedding in the database.
func handleStoreBountyEmbedding(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req StoreEmbeddingRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}

		if req.BountyID == "" || len(req.Embedding.Slice()) == 0 || req.Environment == "" {
			writeBadRequestError(w, errors.New("bounty_id, embedding, and environment are required"))
			return
		}

		params := dbgen.InsertEmbeddingParams{
			BountyID:    req.BountyID,
			Embedding:   req.Embedding,
			Environment: req.Environment,
		}

		if err := querier.InsertEmbedding(r.Context(), params); err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to store embedding: %w", err))
			return
		}

		writeOK(w)
	}
}

// handleDeleteBountyEmbedding handles deleting a specific bounty embedding.
func handleDeleteBountyEmbedding(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bountyID := r.PathValue("bounty_id")
		if bountyID == "" {
			writeBadRequestError(w, errors.New("bounty_id path parameter is required"))
			return
		}

		// The DeleteEmbeddings querier method expects a string like "{id1,id2,id3}"
		// So we format the single BountyID accordingly.
		bountyIDToDelete := fmt.Sprintf("{%s}", bountyID)

		if err := querier.DeleteEmbeddings(r.Context(), bountyIDToDelete); err != nil {
			// We can't easily distinguish between "not found" and other errors here
			// with the current querier.DeleteEmbeddings signature unless it returns a specific error type.
			// For now, assume any error is an internal server error.
			logger.Error("Failed to delete bounty embedding from database via HTTP route", "bounty_id", bountyID, "error", err)
			writeInternalError(logger, w, fmt.Errorf("failed to delete embedding for bounty_id %s: %w", bountyID, err))
			return
		}

		w.WriteHeader(http.StatusOK)
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

		embeddingSlice, err := llmProvider.GenerateEmbedding(r.Context(), queryText, "") // Provider uses its own configured model.
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to generate query embedding: %w", err))
			return
		}
		embeddingVec := pgvector.NewVector(embeddingSlice)

		limit := defaultSearchLimit

		searchParams := dbgen.SearchEmbeddingsParams{
			Embedding:   embeddingVec,
			RowCount:    int32(limit),
			Environment: env,
		}

		results, err := querier.SearchEmbeddings(r.Context(), searchParams)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to search embeddings: %w", err))
			return
		}

		if len(results) == 0 {
			writeJSONResponse(w, []api.BountyListItem{}, http.StatusOK)
			return
		}

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
		var detailedBounties []api.BountyListItem
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
			var tier int64
			if tierPayload, ok := descResp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[abb.BountyTierKey.GetName()]; ok {
				err = converter.GetDefaultDataConverter().FromPayload(tierPayload, &tier)
				if err != nil {
					logger.Warn("Failed to decode BountyTier search attribute for list item", "workflow_id", id, "error", err)
					tier = int64(abb.DefaultBountyTier) // Fallback to default
				}
			} else {
				logger.Warn("BountyTier search attribute not found for list item", "workflow_id", id)
				tier = int64(abb.DefaultBountyTier) // Fallback to default
			}
			var remainingBountyValue float64
			if val, ok := descResp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[abb.BountyValueRemainingKey.GetName()]; ok {
				_ = converter.GetDefaultDataConverter().FromPayload(val, &remainingBountyValue)
			} else {
				remainingBountyValue = input.TotalBounty.ToUSDC() // Default if not found
			}

			var funderWallet string
			if saPayload, ok := descResp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[abb.BountyFunderWalletKey.GetName()]; ok {
				err = converter.GetDefaultDataConverter().FromPayload(saPayload, &funderWallet)
				if err != nil {
					logger.Warn("Failed to decode BountyFunderWallet search attribute for search result", "workflow_id", id, "error", err)
				}
			} else {
				logger.Warn("BountyFunderWallet search attribute not found for search result", "workflow_id", id)
			}

			bountyItem := api.BountyListItem{
				BountyID:             id,
				Title:                input.Title,
				Status:               descResp.WorkflowExecutionInfo.Status.String(),
				Requirements:         input.Requirements, // May be empty if history fetch failed
				BountyPerPost:        input.BountyPerPost.ToUSDC(),
				TotalBounty:          input.TotalBounty.ToUSDC(),
				TotalCharged:         input.TotalCharged.ToUSDC(),
				RemainingBountyValue: remainingBountyValue,
				BountyFunderWallet:   funderWallet,
				PlatformKind:         string(input.Platform),
				ContentKind:          string(input.ContentKind),
				Tier:                 int(tier),
				CreatedAt:            descResp.WorkflowExecutionInfo.StartTime.AsTime(),
				EndAt:                endTime,
			}

			if bountyItem.Status == string(abb.BountyStatusAwaitingFunding) {
				expiresAt := descResp.WorkflowExecutionInfo.StartTime.AsTime().Add(input.PaymentTimeout)
				bountyItem.PaymentTimeoutExpiresAt = &expiresAt
			}

			detailedBounties = append(detailedBounties, bountyItem)
		}

		writeJSONResponse(w, detailedBounties, http.StatusOK)
	}
}

// handlePruneStaleEmbeddings handles the logic for pruning stale embeddings.
// It mirrors the functionality of the pruneStaleEmbeddingsAction CLI command.
func handlePruneStaleEmbeddings(logger *slog.Logger, tc client.Client, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// 1. Fetch active workflow IDs from Temporal
		var activeWorkflowIDs []string
		var nextPageToken []byte
		listQuery := fmt.Sprintf("WorkflowType = 'BountyAssessmentWorkflow' AND ExecutionStatus = 'Running'")

		for {
			resp, errList := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
				Query:         listQuery,
				NextPageToken: nextPageToken,
			})
			if errList != nil {
				writeInternalError(logger, w, fmt.Errorf("failed to list workflows from Temporal: %w", errList))
				return
			}
			for _, execution := range resp.Executions {
				activeWorkflowIDs = append(activeWorkflowIDs, execution.Execution.WorkflowId)
			}
			nextPageToken = resp.NextPageToken
			if len(nextPageToken) == 0 {
				break
			}
		}

		var deletedCount int64

		// 2. Decide deletion strategy based on active workflows
		if len(activeWorkflowIDs) == 0 {
			allDBBountyIDs, errListDB := querier.ListBountyIDs(ctx)
			if errListDB != nil {
				writeInternalError(logger, w, fmt.Errorf("failed to list bounty IDs from embeddings table for deletion: %w", errListDB))
				return
			}
			if len(allDBBountyIDs) == 0 {
				writeJSONResponse(w, map[string]string{"message": "No embeddings found to prune."}, http.StatusOK)
				return
			}

			// Format for ANY($1) - e.g., "{id1,id2}"
			allDBBountyIDsStr := "{" + strings.Join(allDBBountyIDs, ",") + "}"
			if errDel := querier.DeleteEmbeddings(ctx, allDBBountyIDsStr); errDel != nil {
				logger.Error("Failed to delete all embeddings from database.", "error", errDel, "attempted_param_format", allDBBountyIDsStr)
				writeInternalError(logger, w, fmt.Errorf("failed to delete all embeddings: %w", errDel))
				return
			}
			deletedCount = int64(len(allDBBountyIDs))

		} else {
			activeWorkflowIDsStr := "{" + strings.Join(activeWorkflowIDs, ",") + "}"

			if errDelNotIn := querier.DeleteEmbeddingsNotIn(ctx, activeWorkflowIDsStr); errDelNotIn != nil {
				logger.Error("Failed to delete embeddings not in active list.", "error", errDelNotIn, "attempted_param_format", activeWorkflowIDsStr)
				writeInternalError(logger, w, fmt.Errorf("failed to delete embeddings not in active list: %w", errDelNotIn))
				return
			}
		}

		if len(activeWorkflowIDs) == 0 {
			writeJSONResponse(w, map[string]interface{}{"message": "Successfully deleted all stale embeddings.", "deleted_count": deletedCount}, http.StatusOK)
		} else {
			writeJSONResponse(w, map[string]string{"message": "Successfully pruned stale embeddings not in active workflows."}, http.StatusOK)
		}
	}
}

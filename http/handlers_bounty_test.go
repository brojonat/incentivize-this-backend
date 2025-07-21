package http

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/solana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/mocks"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHandleListBounties(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name           string
		setupMock      func(*mocks.Client)
		expectedStatus int
		expectedBody   interface{}
	}{
		{
			name: "successful list bounties",
			setupMock: func(mtc *mocks.Client) {
				// Setup ListWorkflow mock
				execution := &commonpb.WorkflowExecution{
					WorkflowId: "test-workflow-1",
					RunId:      "test-run-1",
				}
				executions := []*workflowpb.WorkflowExecutionInfo{
					{
						Execution: execution,
						Status:    enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
						StartTime: timestamppb.New(now),
						SearchAttributes: &commonpb.SearchAttributes{
							IndexedFields: map[string]*commonpb.Payload{
								"BountyStatus":         mustPayload(converter.GetDefaultDataConverter(), string(abb.BountyStatusListening)),
								"BountyFunderWallet":   mustPayload(converter.GetDefaultDataConverter(), "test-funder"),
								"BountyPlatform":       mustPayload(converter.GetDefaultDataConverter(), string(abb.PlatformReddit)),
								"BountyValueRemaining": mustPayload(converter.GetDefaultDataConverter(), 0.0),
								"BountyTier":           mustPayload(converter.GetDefaultDataConverter(), int64(abb.DefaultBountyTier)),
							},
						},
					},
				}
				mtc.On("ListWorkflow", mock.Anything, mock.AnythingOfType("*workflowservice.ListWorkflowExecutionsRequest")).
					Return(&workflowservice.ListWorkflowExecutionsResponse{
						Executions: executions,
					}, nil)

				// --- Mock GetWorkflowHistory ---
				mockIter := &mocks.HistoryEventIterator{}
				// Define the input payload for the workflow
				bountyPerPost, _ := solana.NewUSDCAmount(10.0)
				totalBounty, _ := solana.NewUSDCAmount(100.0)
				expectedInput := abb.BountyAssessmentWorkflowInput{
					Requirements:  []string{"Test requirement 1", "Test requirement 2"},
					BountyPerPost: bountyPerPost,
					TotalBounty:   totalBounty,
					Platform:      abb.PlatformReddit,
					// Add other fields matching the expected input in the handler
				}
				inputPayload, err := converter.GetDefaultDataConverter().ToPayload(expectedInput)
				assert.NoError(t, err) // Use assert within the test setup

				// Create the history event
				historyEvent := &historypb.HistoryEvent{
					EventId:   1,
					EventTime: timestamppb.Now(),
					EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
						WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
							Input: &commonpb.Payloads{
								Payloads: []*commonpb.Payload{inputPayload},
							},
							// Add other necessary attributes if needed
						},
					},
				}

				// Configure the mock iterator
				var nextCalls int
				mockIter.On("HasNext").Return(func() bool {
					nextCalls++
					return nextCalls <= 1 // Only return true for the first call
				})
				mockIter.On("Next").Return(historyEvent, nil).Once() // Return the event once

				// Add the mock expectation for GetWorkflowHistory
				mtc.On("GetWorkflowHistory",
					mock.Anything, // context
					"test-workflow-1",
					"test-run-1",
					false,
					enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
				).Return(mockIter, nil)
				// --- End Mock GetWorkflowHistory ---
			},
			expectedStatus: http.StatusOK,
			expectedBody: []api.BountyListItem{
				{
					BountyID:             "test-workflow-1",
					Status:               "Listening",
					Requirements:         []string{"Test requirement 1", "Test requirement 2"},
					BountyPerPost:        10.0,
					TotalBounty:          100.0,
					BountyFunderWallet:   "test-funder",
					PlatformKind:         string(abb.PlatformReddit),
					ContentKind:          "",
					Tier:                 int(abb.DefaultBountyTier),
					CreatedAt:            now.UTC(),
					EndAt:                time.Time{},
					RemainingBountyValue: 0.0,
				},
			},
		},
		{
			name: "error listing workflows",
			setupMock: func(mtc *mocks.Client) {
				mtc.On("ListWorkflow", mock.Anything, mock.Anything).
					Return(nil, assert.AnError)
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody: api.DefaultJSONResponse{
				Message: "",
				Error:   "internal error",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			mockClient := &mocks.Client{}
			tt.setupMock(mockClient)

			// Create a ResponseRecorder
			rec := httptest.NewRecorder()

			// Create the handler with the mock client and a test environment
			handler := handleListBounties(slog.Default(), mockClient, "test")

			// Serve the HTTP request
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bounties", nil))

			// Assert
			assert.Equal(t, tt.expectedStatus, rec.Code)

			var response interface{}
			if tt.expectedStatus == http.StatusOK {
				var bounties []api.BountyListItem
				err := json.NewDecoder(rec.Body).Decode(&bounties)
				assert.NoError(t, err)
				response = bounties
			} else {
				var resp api.DefaultJSONResponse
				err := json.NewDecoder(rec.Body).Decode(&resp)
				assert.NoError(t, err)
				response = resp
			}

			assert.Equal(t, tt.expectedBody, response)
			mockClient.AssertExpectations(t)
		})
	}
}

// Helper function to panic on payload conversion error in tests
func mustPayload(dc converter.DataConverter, value interface{}) *commonpb.Payload {
	payload, err := dc.ToPayload(value)
	if err != nil {
		panic(fmt.Sprintf("Failed to convert value %v to payload: %v", value, err))
	}
	return payload
}

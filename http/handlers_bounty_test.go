package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/solana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
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
					},
				}
				mtc.On("ListWorkflow", mock.Anything, mock.Anything).
					Return(&workflowservice.ListWorkflowExecutionsResponse{
						Executions: executions,
					}, nil)

				// Setup GetWorkflow mock
				mockRun := &mocks.WorkflowRun{}
				mockRun.On("Get", mock.Anything, mock.Anything).
					Return(nil).
					Run(func(args mock.Arguments) {
						input := args.Get(1).(*abb.BountyAssessmentWorkflowInput)
						bountyPerPost, _ := solana.NewUSDCAmount(10.0)
						totalBounty, _ := solana.NewUSDCAmount(100.0)
						*input = abb.BountyAssessmentWorkflowInput{
							Requirements:      []string{"Test requirement 1", "Test requirement 2"},
							BountyPerPost:     bountyPerPost,
							TotalBounty:       totalBounty,
							BountyOwnerWallet: "test-owner",
							PlatformType:      abb.PlatformReddit,
						}
					})

				mtc.On("GetWorkflow", mock.Anything, "test-workflow-1", "test-run-1").
					Return(mockRun)
			},
			expectedStatus: http.StatusOK,
			expectedBody: []BountyListItem{
				{
					WorkflowID:        "test-workflow-1",
					Status:            "Running",
					Requirements:      []string{"Test requirement 1", "Test requirement 2"},
					BountyPerPost:     10.0,
					TotalBounty:       100.0,
					BountyOwnerWallet: "test-owner",
					PlatformType:      abb.PlatformReddit,
					CreatedAt:         now.UTC(),
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
			expectedBody: DefaultJSONResponse{
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

			handler := handleListBounties(slog.Default(), mockClient)
			req := httptest.NewRequest(http.MethodGet, "/bounties", nil)
			w := httptest.NewRecorder()

			// Execute
			handler.ServeHTTP(w, req)

			// Assert
			assert.Equal(t, tt.expectedStatus, w.Code)

			var response interface{}
			if tt.expectedStatus == http.StatusOK {
				var bounties []BountyListItem
				err := json.NewDecoder(w.Body).Decode(&bounties)
				assert.NoError(t, err)
				response = bounties
			} else {
				var resp DefaultJSONResponse
				err := json.NewDecoder(w.Body).Decode(&resp)
				assert.NoError(t, err)
				response = resp
			}

			assert.Equal(t, tt.expectedBody, response)
			mockClient.AssertExpectations(t)
		})
	}
}

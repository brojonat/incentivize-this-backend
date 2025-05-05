package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"go.temporal.io/sdk/client"
)

// --- Struct Definitions from example_webhook.go ---

// Common fields for all webhook events
type BMCWebhookEvent struct {
	Type     string          `json:"type"`
	LiveMode bool            `json:"live_mode"`
	Attempt  int             `json:"attempt"`
	Created  int64           `json:"created"`
	EventID  int             `json:"event_id"`
	Data     json.RawMessage `json:"data"`
}

// Common payment-related fields shared across multiple event types
type BMCPaymentData struct {
	ID                 int     `json:"id"`
	Amount             float64 `json:"amount"`
	Object             string  `json:"object"`
	Status             string  `json:"status"`
	Message            string  `json:"message"`
	Currency           string  `json:"currency"`
	Refunded           string  `json:"refunded"`
	CreatedAt          int64   `json:"created_at"`
	NoteHidden         string  `json:"note_hidden"`
	RefundedAt         int64   `json:"refunded_at"`
	SupportNote        *string `json:"support_note"`
	SupportType        string  `json:"support_type"`
	SupporterName      string  `json:"supporter_name"`
	SupporterID        int     `json:"supporter_id"`
	SupporterEmail     string  `json:"supporter_email"`
	TransactionID      string  `json:"transaction_id"`
	ApplicationFee     string  `json:"application_fee"`
	TotalAmountCharged string  `json:"total_amount_charged"`
	CoffeeCount        int     `json:"coffee_count"`
}

// Common subscription-related fields shared between recurring donations and memberships
type BMCSubscriptionData struct {
	ID                 int     `json:"id"`
	Amount             float64 `json:"amount"`
	Object             string  `json:"object"`
	Paused             string  `json:"paused"`
	Status             string  `json:"status"`
	Canceled           string  `json:"canceled"`
	Currency           string  `json:"currency"`
	PspID              string  `json:"psp_id"`
	DurationType       string  `json:"duration_type"`
	StartedAt          int64   `json:"started_at"`
	CanceledAt         int64   `json:"canceled_at"`
	NoteHidden         bool    `json:"note_hidden"`
	SupportNote        *string `json:"support_note"`
	SupporterName      string  `json:"supporter_name"`
	SupporterID        int     `json:"supporter_id"`
	SupporterEmail     string  `json:"supporter_email"`
	CurrentPeriodEnd   int64   `json:"current_period_end"`
	CurrentPeriodStart int64   `json:"current_period_start"`
	SupporterFeedback  *string `json:"supporter_feedback,omitempty"`
	CancelAtPeriodEnd  *string `json:"cancel_at_period_end,omitempty"`
}

// Additional fields for membership events
type BMCMembershipData struct {
	BMCSubscriptionData
	MembershipLevelID   int    `json:"membership_level_id"`
	MembershipLevelName string `json:"membership_level_name"`
	CreatedAt           int64  `json:"created_at"`
	CanceledAt          int64  `json:"canceled_at"`
	CurrentPeriodStart  int64  `json:"current_period_start"`
	CurrentPeriodEnd    int64  `json:"current_period_end"`
}

// Extra purchase item
type BMCExtra struct {
	ID              int      `json:"id"`
	Title           string   `json:"title"`
	Amount          string   `json:"amount"`
	Quantity        int      `json:"quantity"`
	Object          string   `json:"object"`
	Currency        string   `json:"currency"`
	Description     string   `json:"description"`
	ExtraQuestion   string   `json:"extra_question"`
	QuestionAnswers []string `json:"question_answers"`
}

// Extra purchase data
type BMCExtraPurchaseData struct {
	BMCPaymentData
	Extras     []BMCExtra `json:"extras"`
	CreatedAt  int64      `json:"created_at"`
	RefundedAt int64      `json:"refunded_at"`
}

// Shipping address for commissions
type BMCShippingAddress struct {
	Zip     string `json:"zip"`
	City    string `json:"city"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Street  string `json:"street"`
	Country string `json:"country"`
}

// Commission details
type BMCCommission struct {
	ID               int                `json:"id"`
	Name             string             `json:"name"`
	Addons           map[string]string  `json:"addons"`
	Amount           string             `json:"amount"`
	Object           string             `json:"object"`
	Attachments      []string           `json:"attachments"`
	Description      string             `json:"description"`
	RefundReason     *string            `json:"refund_reason"`
	ShippingPrice    string             `json:"shipping_price"`
	AdditionalInfo   string             `json:"additional_info"`
	DiscountAmount   float64            `json:"discount_amount"`
	ShippingAddress  BMCShippingAddress `json:"shipping_address"`
	TotalOrderAmount string             `json:"total_order_amount"`
	CreatedAt        int64              `json:"created_at"`
	RefundedAt       int64              `json:"refunded_at"`
}

// Commission order data
type BMCCommissionOrderData struct {
	BMCPaymentData
	Commission BMCCommission `json:"commission"`
}

// Wishlist item
type BMCWishlistItem struct {
	ID          int     `json:"id"`
	Price       string  `json:"price"`
	Title       string  `json:"title"`
	Object      string  `json:"object"`
	Completed   bool    `json:"completed"`
	TotalPaid   float64 `json:"total_paid"`
	Description string  `json:"description"`
}

// Wishlist payment data
type BMCWishlistPaymentData struct {
	BMCPaymentData
	Wishlist   BMCWishlistItem `json:"wishlist"`
	CreatedAt  int64           `json:"created_at"`
	RefundedAt int64           `json:"refunded_at"`
}

// --- End Struct Definitions ---

// handleBMCWebhook processes incoming webhook requests from Buy Me a Coffee
func handleBMCWebhook(logger *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read and log raw body
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Error("Error reading webhook body", "error", err)
			writeBadRequestError(w, fmt.Errorf("failed to read request body"))
			return
		}
		// Use logger passed into the handler
		logger.Debug("Raw webhook body", "body", string(bodyBytes))
		// Restore the body so it can be read again by json.Decoder
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// First decode the common event structure
		var event BMCWebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			logger.Error("Error decoding webhook event", "error", err)
			writeBadRequestError(w, fmt.Errorf("invalid webhook payload"))
			return
		}

		// Log the event type and metadata
		logger.Debug("Received webhook event",
			"type", event.Type,
			"live_mode", event.LiveMode,
			"event_id", event.EventID,
			"attempt", event.Attempt,
			"created", event.Created)

		// Debug logging for webhook request
		logger.Debug("Webhook request details",
			"method", r.Method,
			"url", r.URL.String(),
			"remote_addr", r.RemoteAddr,
			"headers", formatHeaders(r.Header))

		// Handle different event types
		switch event.Type {
		// Donation events
		case "donation.created", "donation.refunded":
			var donationData BMCPaymentData
			if err := json.Unmarshal(event.Data, &donationData); err != nil {
				logger.Error("Error decoding donation data", "error", err)
				writeBadRequestError(w, fmt.Errorf("invalid donation data"))
				return
			}

			if event.Type == "donation.created" && donationData.Status == "succeeded" {
				logger.Info("Processing successful donation",
					"amount", donationData.Amount,
					"supporter_email", donationData.SupporterEmail,
					"supporter_name", donationData.SupporterName,
					"transaction_id", donationData.TransactionID)

				// Example: Trigger a workflow, grant access, etc.
				token, err := createUserToken(donationData.SupporterEmail, time.Now().Add(time.Duration(donationData.CoffeeCount)*7*24*time.Hour))
				if err != nil {
					logger.Error("Failed to create user token", "error", err)
					writeInternalError(logger, w, fmt.Errorf("failed to create user token"))
					return
				}
				// Start EmailTokenWorkflow
				wfInput := abb.EmailTokenWorkflowInput{
					Email: donationData.SupporterEmail,
					Token: token,
				}
				wfOptions := client.StartWorkflowOptions{
					ID:        fmt.Sprintf("email-token-%s-%d", donationData.TransactionID, event.Attempt),
					TaskQueue: os.Getenv(EnvTaskQueue), // Assuming TASK_QUEUE env var is set
				}
				_, err = tc.ExecuteWorkflow(r.Context(), wfOptions, abb.EmailTokenWorkflow, wfInput)
				if err != nil {
					logger.Error("Failed to start EmailTokenWorkflow", "error", err, "workflowID", wfOptions.ID)
					// Decide if this should return internal error or just log
					writeInternalError(logger, w, fmt.Errorf("failed to initiate email process"))
					return
				}
				logger.Info("Successfully started EmailTokenWorkflow", "workflowID", wfOptions.ID)

				// // --- Token Generation/Emailing (Commented out - from reference project) ---
				// // Create bearer token with expiration based on coffee count

			} else if event.Type == "donation.refunded" && donationData.Status == "refunded" {
				logger.Info("Processing donation refund",
					"amount", donationData.Amount,
					"supporter_email", donationData.SupporterEmail,
					"transaction_id", donationData.TransactionID,
					"refunded_at", donationData.RefundedAt)
				// TODO: Handle refund for Affiliate Bounty Board
			}

		// Recurring donation events
		case "recurring_donation.started", "recurring_donation.updated", "recurring_donation.cancelled":
			var recurringData BMCSubscriptionData
			if err := json.Unmarshal(event.Data, &recurringData); err != nil {
				logger.Error("Error decoding recurring donation data", "error", err)
				writeBadRequestError(w, fmt.Errorf("invalid recurring donation data"))
				return
			}

			switch event.Type {
			case "recurring_donation.started":
				if recurringData.Status == "active" {
					logger.Info("Processing new recurring donation",
						"amount", recurringData.Amount,
						"supporter_email", recurringData.SupporterEmail,
						"psp_id", recurringData.PspID,
						"period_end", recurringData.CurrentPeriodEnd) // Keep as timestamp for logging simplicity
					// TODO: Handle new subscription for Affiliate Bounty Board
				}
			case "recurring_donation.updated":
				logger.Info("Processing recurring donation update",
					"amount", recurringData.Amount,
					"supporter_email", recurringData.SupporterEmail,
					"psp_id", recurringData.PspID,
					"paused", recurringData.Paused,
					"cancel_at_period_end", recurringData.CancelAtPeriodEnd)
				// TODO: Handle subscription update for Affiliate Bounty Board
			case "recurring_donation.cancelled":
				logger.Info("Processing recurring donation cancellation",
					"supporter_email", recurringData.SupporterEmail,
					"psp_id", recurringData.PspID,
					"feedback", recurringData.SupporterFeedback)
				// TODO: Handle subscription cancellation for Affiliate Bounty Board
			}

		// Membership events
		case "membership.started", "membership.updated", "membership.cancelled":
			var membershipData BMCMembershipData
			if err := json.Unmarshal(event.Data, &membershipData); err != nil {
				logger.Error("Error decoding membership data", "error", err)
				writeBadRequestError(w, fmt.Errorf("invalid membership data"))
				return
			}

			switch event.Type {
			case "membership.started":
				if membershipData.Status == "active" {
					logger.Info("Processing new membership",
						"amount", membershipData.Amount,
						"supporter_email", membershipData.SupporterEmail,
						"level_name", membershipData.MembershipLevelName,
						"psp_id", membershipData.PspID)
					// TODO: Handle new membership for Affiliate Bounty Board
				}
			case "membership.updated":
				logger.Info("Processing membership update",
					"supporter_email", membershipData.SupporterEmail,
					"level_name", membershipData.MembershipLevelName,
					"paused", membershipData.Paused,
					"cancel_at_period_end", membershipData.CancelAtPeriodEnd)
				// TODO: Handle membership update for Affiliate Bounty Board
			case "membership.cancelled":
				logger.Info("Processing membership cancellation",
					"supporter_email", membershipData.SupporterEmail,
					"level_name", membershipData.MembershipLevelName,
					"feedback", membershipData.SupporterFeedback)
				// TODO: Handle membership cancellation for Affiliate Bounty Board
			}

		// Extra purchase events
		case "extra_purchase.created", "extra_purchase.refunded":
			var extraData BMCExtraPurchaseData
			if err := json.Unmarshal(event.Data, &extraData); err != nil {
				logger.Error("Error decoding extra purchase data", "error", err)
				writeBadRequestError(w, fmt.Errorf("invalid extra purchase data"))
				return
			}

			if event.Type == "extra_purchase.created" && extraData.Status == "succeeded" {
				logger.Info("Processing extra purchase",
					"amount", extraData.Amount,
					"supporter_email", extraData.SupporterEmail,
					"extras", len(extraData.Extras))
				// TODO: Handle extra purchase for Affiliate Bounty Board
			} else if event.Type == "extra_purchase.refunded" && extraData.Status == "refunded" {
				logger.Info("Processing extra purchase refund",
					"amount", extraData.Amount,
					"supporter_email", extraData.SupporterEmail,
					"refunded_at", extraData.RefundedAt)
				// TODO: Handle extra refund for Affiliate Bounty Board
			}

		// Commission events
		case "commission_order.created", "commission_order.refunded":
			var commissionData BMCCommissionOrderData
			if err := json.Unmarshal(event.Data, &commissionData); err != nil {
				logger.Error("Error decoding commission data", "error", err)
				writeBadRequestError(w, fmt.Errorf("invalid commission data"))
				return
			}

			if event.Type == "commission_order.created" && commissionData.Status == "succeeded" {
				logger.Info("Processing commission order",
					"amount", commissionData.Amount,
					"supporter_email", commissionData.SupporterEmail,
					"commission_name", commissionData.Commission.Name)
				// TODO: Handle commission order for Affiliate Bounty Board
			} else if event.Type == "commission_order.refunded" && commissionData.Status == "refunded" {
				logger.Info("Processing commission refund",
					"amount", commissionData.Amount,
					"supporter_email", commissionData.SupporterEmail,
					"refund_reason", commissionData.Commission.RefundReason)
				// TODO: Handle commission refund for Affiliate Bounty Board
			}

		// Wishlist events
		case "wishlist_payment.created", "wishlist_payment.refunded":
			var wishlistData BMCWishlistPaymentData
			if err := json.Unmarshal(event.Data, &wishlistData); err != nil {
				logger.Error("Error decoding wishlist data", "error", err)
				writeBadRequestError(w, fmt.Errorf("invalid wishlist data"))
				return
			}

			if event.Type == "wishlist_payment.created" && wishlistData.Status == "succeeded" {
				logger.Info("Processing wishlist payment",
					"amount", wishlistData.Amount,
					"supporter_email", wishlistData.SupporterEmail,
					"wishlist_item", wishlistData.Wishlist.Title)
				// TODO: Handle wishlist payment for Affiliate Bounty Board
			} else if event.Type == "wishlist_payment.refunded" && wishlistData.Status == "refunded" {
				logger.Info("Processing wishlist refund",
					"amount", wishlistData.Amount,
					"supporter_email", wishlistData.SupporterEmail,
					"refunded_at", wishlistData.RefundedAt)
				// TODO: Handle wishlist refund for Affiliate Bounty Board
			}

		default:
			logger.Warn("Unhandled event type", "type", event.Type) // Use Warn for unhandled but valid events
			// Respond OK even if unhandled, as BMC expects 200
			// writeBadRequestError(w, fmt.Errorf("unknown event type %s", event.Type))
		}

		// Respond OK to BMC
		writeOK(w)
	}
}

// Helper function to format headers for logging (Copied from reference)
func formatHeaders(headers http.Header) string {
	var headerStrings []string
	for name, values := range headers {
		// Skip sensitive headers if needed
		if name == "Authorization" || name == "Cookie" || name == "X-Api-Key" { // Added X-Api-Key
			headerStrings = append(headerStrings, fmt.Sprintf("%s: [REDACTED]", name))
			continue
		}
		headerStrings = append(headerStrings, fmt.Sprintf("%s: %v", name, values))
	}
	return strings.Join(headerStrings, "; ")
}

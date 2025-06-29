package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
)

const gumroadAPIBaseURL = "https://api.gumroad.com/v2/"
const EnvGumroadAPIKey = "GUMROAD_API_KEY"

// Client is a client for the Gumroad API.
type Client struct {
	httpClient *http.Client
}

// NewGumroadClient creates a new Gumroad API client.
func NewGumroadClient() (*Client, error) {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// SalesResponse is the response from the /sales endpoint.
type SalesResponse struct {
	Success bool                            `json:"success"`
	Sales   []dbgen.InsertGumroadSaleParams `json:"sales"`
}

// GetSales fetches sales from the Gumroad API.
func (c *Client) GetSales(productID, accessToken string, after time.Duration, before, afterDate string) (*SalesResponse, error) {
	u, err := url.Parse(gumroadAPIBaseURL + "sales")
	if err != nil {
		return nil, fmt.Errorf("failed to parse gumroad api url: %w", err)
	}

	q := u.Query()
	q.Set("access_token", accessToken)
	if productID != "" {
		q.Set("product_id", productID)
	}
	if after > 0 {
		// Gumroad API expects 'after' as a date string 'YYYY-MM-DD'.
		afterTimestamp := time.Now().Add(-after)
		q.Set("after", afterTimestamp.Format("2006-01-02"))
	}
	if before != "" {
		q.Set("before", before)
	}
	if afterDate != "" {
		q.Set("after", afterDate)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create gumroad request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get gumroad sales: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from gumroad: %d", resp.StatusCode)
	}

	var salesResp SalesResponse
	if err := json.NewDecoder(resp.Body).Decode(&salesResp); err != nil {
		return nil, fmt.Errorf("failed to decode gumroad sales response: %w", err)
	}

	return &salesResp, nil
}

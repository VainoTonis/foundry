package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client provides a base HTTP client for interacting with the foundry API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// ErrorResponse represents an API error response.
type ErrorResponse struct {
	Error  string `json:"error"`
	Status int    `json:"status,omitempty"`
}

// ---- Shared Types ----

// Project represents a foundry project.
type Project struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	RepoPath  string    `json:"repo_path"`
	CreatedAt time.Time `json:"created_at"`
}

// Plan represents a foundry plan.
type Plan struct {
	ID        int64     `json:"id"`
	ProjectID *int64    `json:"project_id,omitempty"`
	RepoName  string    `json:"repo_name"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PlanStep represents a single step in a plan.
type PlanStep struct {
	ID            int64     `json:"id"`
	PlanID        int64     `json:"plan_id"`
	Position      int       `json:"position"`
	Text          string    `json:"text"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ParallelGroup *int      `json:"parallel_group,omitempty"`
}

// CreateStepInput is used when creating a new plan step.
type CreateStepInput struct {
	Position      int    `json:"position"`
	Text          string `json:"text"`
	ParallelGroup *int   `json:"parallel_group,omitempty"`
}

// ---- Constructor ----

// NewClient creates a new API client with the given base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ---- Request/Response Helpers ----

// do performs an HTTP request and handles the response.
// If the response status is not 2xx, it returns an error.
func (c *Client) do(method, path string, body interface{}) (*http.Response, error) {
	url := c.baseURL + path
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Check for non-2xx status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		var errResp ErrorResponse
		_ = json.Unmarshal(data, &errResp)
		if errResp.Error == "" {
			errResp.Error = string(data)
		}
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, errResp.Error)
	}

	return resp, nil
}

// Get performs a GET request and decodes the JSON response into v.
func (c *Client) Get(path string, v interface{}) error {
	resp, err := c.do(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// Post performs a POST request with the given body and decodes the JSON response into v.
func (c *Client) Post(path string, body, v interface{}) error {
	resp, err := c.do(http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// Patch performs a PATCH request with the given body and decodes the JSON response into v.
func (c *Client) Patch(path string, body, v interface{}) error {
	resp, err := c.do(http.MethodPatch, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// Delete performs a DELETE request. It returns any error from the API.
// For 204 No Content responses, it returns nil after closing the response body.
func (c *Client) Delete(path string) error {
	url := c.baseURL + path
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check for non-2xx status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		var errResp ErrorResponse
		_ = json.Unmarshal(data, &errResp)
		if errResp.Error == "" {
			errResp.Error = string(data)
		}
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, errResp.Error)
	}

	return nil
}

package httpclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a generic HTTP client with retry and exponential backoff.
type Client struct {
	httpClient  *http.Client
	retryMax    int
	backoffBase time.Duration
}

// NewClient creates a new HTTP client.
func NewClient(timeout time.Duration, retryMax int, backoffBase time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		retryMax:    retryMax,
		backoffBase: backoffBase,
	}
}

// Do performs an HTTP request with retries on retryable errors (network, 5xx).
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retryMax; attempt++ {
		if attempt > 0 {
			// exponential backoff: base * 2^(attempt-1)
			backoff := c.backoffBase * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, err := c.httpClient.Do(req.WithContext(ctx))
		if err != nil {
			lastErr = err
			continue
		}
		// Retry on server errors (5xx)
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			resp.Body.Close()
			lastErr = fmt.Errorf("server error: %s", resp.Status)
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("failed after %d retries: %w", c.retryMax, lastErr)
}

// Get is a convenience method for GET requests.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, req)
}

// Post is a convenience method for POST requests with body.
func (c *Client) Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(ctx, req)
}
package woo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"tgsc/internal/domain"
)

// Client handles communication with WooCommerce REST API.
type Client struct {
	baseURL         string
	consumerKey     string
	consumerSecret  string
	httpClient      *http.Client
	rateLimiter     *rate.Limiter
	batchCreateSize int
	batchUpdateSize int
	dryRun          bool // اگر true باشد، درخواستی به Woo ارسال نمی‌شود
}

// NewClient creates a new WooCommerce client.
func NewClient(
	baseURL, consumerKey, consumerSecret string,
	timeout time.Duration,
	rateLimit int,
	batchCreateSize, batchUpdateSize int,
	dryRun bool,
) *Client {
	return &Client{
		baseURL:         baseURL,
		consumerKey:     consumerKey,
		consumerSecret:  consumerSecret,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		rateLimiter:     rate.NewLimiter(rate.Limit(rateLimit), 1),
		batchCreateSize: batchCreateSize,
		batchUpdateSize: batchUpdateSize,
		dryRun:          dryRun,
	}
}

// doRequest performs an authenticated HTTP request.
func (c *Client) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	// Wait for rate limiter
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	url := fmt.Sprintf("%s%s", c.baseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Basic Auth using Consumer Key and Consumer Secret (not query string)
	req.SetBasicAuth(c.consumerKey, c.consumerSecret)
	req.Header.Set("Content-Type", "application/json")

	return c.httpClient.Do(req)
}

// BatchCreateProducts creates multiple products in a single request.
func (c *Client) BatchCreateProducts(ctx context.Context, products []*domain.Product) (*BatchResult, error) {
	if c.dryRun {
		log.Printf("🔹 DRY RUN: درخواست BatchCreate به WooCommerce ارسال نشد (تعداد محصولات: %d)", len(products))
		// برگرداندن پاسخ ساختگی (همه موفق)
		result := &BatchResult{
			SuccessSet: make(map[string]bool),
			FailedIDs:  make(map[string]string),
		}
		for _, p := range products {
			result.SuccessSet[p.SourceID] = true
		}
		return result, nil
	}

	wooProducts := make([]*WooProduct, len(products))
	for i, p := range products {
		wooProducts[i] = MapDomainToWoo(p)
	}
	payload := BatchCreatePayload{Create: wooProducts}
	return c.doBatchRequest(ctx, "POST", "/products/batch", payload)
}

// BatchUpdateProducts updates multiple products in a single request.
func (c *Client) BatchUpdateProducts(ctx context.Context, products []*domain.Product) (*BatchResult, error) {
	if c.dryRun {
		log.Printf("🔹 DRY RUN: درخواست BatchUpdate به WooCommerce ارسال نشد (تعداد محصولات: %d)", len(products))
		result := &BatchResult{
			SuccessSet: make(map[string]bool),
			FailedIDs:  make(map[string]string),
		}
		for _, p := range products {
			result.SuccessSet[p.SourceID] = true
		}
		return result, nil
	}

	wooProducts := make([]*WooProduct, len(products))
	for i, p := range products {
		wooProducts[i] = MapDomainToWoo(p)
	}
	payload := BatchUpdatePayload{Update: wooProducts}
	return c.doBatchRequest(ctx, "POST", "/products/batch", payload)
}

// doBatchRequest is a generic batch request handler.
func (c *Client) doBatchRequest(ctx context.Context, method, endpoint string, payload interface{}) (*BatchResult, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	resp, err := c.doRequest(ctx, method, endpoint, bodyBytes)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// WooCommerce batch endpoint returns HTTP 200 even if some items fail
	var batchResp BatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&batchResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result := &BatchResult{
		SuccessSet: make(map[string]bool),
		FailedIDs:  make(map[string]string),
	}
	// Process created products
	for _, created := range batchResp.Create {
		if created.ID != 0 && created.SKU != "" {
			result.SuccessSet[created.SKU] = true
		}
	}
	// Process updated products
	for _, updated := range batchResp.Update {
		if updated.ID != 0 && updated.SKU != "" {
			result.SuccessSet[updated.SKU] = true
		}
	}
	// Process errors
	for _, errItem := range batchResp.Errors {
		if errItem.SKU != "" {
			result.FailedIDs[errItem.SKU] = errItem.Message
		}
	}
	return result, nil
}
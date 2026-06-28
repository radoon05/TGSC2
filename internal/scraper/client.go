package scraper

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"tgsc/internal/config"
	"tgsc/internal/domain"
)

// Client handles scraping products from the external source
type Client struct {
	httpClient  *http.Client
	baseURL     string
	rateLimiter *rate.Limiter
	retryMax    int
	retryBackoff time.Duration
	pageSize    int
	parser      *Parser
}

// NewClient creates a new scraper client with the given configuration
func NewClient(cfg *config.ScraperConfig) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		baseURL:      cfg.BaseURL,
		rateLimiter:  rate.NewLimiter(rate.Limit(cfg.RateLimit), 1),
		retryMax:     cfg.RetryMax,
		retryBackoff: cfg.RetryBackoff,
		pageSize:     cfg.PageSize,
		parser:       NewParser(),
	}
}

// FetchProducts scrapes all products from the source (with pagination) and returns domain products.
func (c *Client) FetchProducts(ctx context.Context) ([]*domain.Product, error) {
	var allProducts []*domain.Product
	page := 1

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// rate limit wait
		if err := c.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter: %w", err)
		}

		// build URL with pagination
		url := fmt.Sprintf("%s?page=%d&per_page=%d", c.baseURL, page, c.pageSize)

		var resp *http.Response
		var err error
		// retry loop
		for attempt := 0; attempt <= c.retryMax; attempt++ {
			if attempt > 0 {
				// backoff before retry
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(c.retryBackoff * time.Duration(attempt)):
				}
			}

			req, errReq := http.NewRequestWithContext(ctx, "GET", url, nil)
			if errReq != nil {
				return nil, errReq
			}
			resp, err = c.httpClient.Do(req)
			if err == nil && resp.StatusCode < 500 {
				break // success or client error (4xx) break retry
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
		if err != nil {
			return nil, fmt.Errorf("failed after %d retries: %w", c.retryMax, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status: %s", resp.Status)
		}

		// use parser to decode products
		products, err := c.parser.ParseProducts(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("parse error: %w", err)
		}
		allProducts = append(allProducts, products...)

		// check if pagination finished (if less than pageSize returned)
		if len(products) < c.pageSize {
			break
		}
		page++
	}

	return allProducts, nil
}
package woo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"tgsc/internal/domain"
)

// ============================================================
//  Client
// ============================================================

type Client struct {
	baseURL        string
	consumerKey    string
	consumerSecret string
	httpClient     *http.Client
	rateLimiter    *rate.Limiter
	dryRun         bool
}

// NewClient با ۷ پارامتر (بدون batchCreateSize/batchUpdateSize)
func NewClient(
	baseURL, consumerKey, consumerSecret string,
	timeout time.Duration,
	rateLimit int,
	dryRun bool,
) *Client {
	return &Client{
		baseURL:        baseURL,
		consumerKey:    consumerKey,
		consumerSecret: consumerSecret,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		rateLimiter: rate.NewLimiter(rate.Limit(rateLimit), 1),
		dryRun:      dryRun,
	}
}

// ============================================================
//  doRequest
// ============================================================

func (c *Client) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	url := fmt.Sprintf("%s%s", c.baseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.consumerKey, c.consumerSecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ============================================================
//  BatchCreateProducts
// ============================================================

func (c *Client) BatchCreateProducts(ctx context.Context, products []*domain.Product) (*BatchResult, error) {
	if c.dryRun {
		log.Printf("🔹 DRY RUN: درخواست BatchCreate به WooCommerce ارسال نشد (تعداد محصولات: %d)", len(products))
		// در dry-run، هیچ موفقیتی جعل نمی‌کنیم
		return &BatchResult{
			SuccessSet:  make(map[string]bool),
			FailedIDs:   make(map[string]string),
			SKUToWooID:  make(map[string]int64),
		}, nil
	}

	wooProducts := make([]*WooProduct, 0, len(products))
	for _, p := range products {
		wooProducts = append(wooProducts, MapToCreate(p))
	}
	payload := BatchCreatePayload{Create: wooProducts}
	return c.doBatchRequest(ctx, "POST", "/products/batch", payload, products)
}

// ============================================================
//  BatchUpdateProducts
// ============================================================

func (c *Client) BatchUpdateProducts(ctx context.Context, products []*domain.Product) (*BatchResult, error) {
	if c.dryRun {
		log.Printf("🔹 DRY RUN: درخواست BatchUpdate به WooCommerce ارسال نشد (تعداد محصولات: %d)", len(products))
		return &BatchResult{
			SuccessSet:  make(map[string]bool),
			FailedIDs:   make(map[string]string),
			SKUToWooID:  make(map[string]int64),
		}, nil
	}

	wooProducts := make([]*WooProduct, 0, len(products))
	for _, p := range products {
		wooProducts = append(wooProducts, MapToUpdate(p))
	}
	payload := BatchUpdatePayload{Update: wooProducts}
	return c.doBatchRequest(ctx, "POST", "/products/batch", payload, products)
}

// ============================================================
//  doBatchRequest (با چک StatusCode و parse صحیح)
// ============================================================

func (c *Client) doBatchRequest(ctx context.Context, method, endpoint string, payload interface{}, products []*domain.Product) (*BatchResult, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	resp, err := c.doRequest(ctx, method, endpoint, bodyBytes)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 🔥 ۱. چک کردن StatusCode
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("woocommerce API error: status=%d, body=%s", resp.StatusCode, string(body))
	}

	// 🔥 ۲. Decode پاسخ
	var batchResp BatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&batchResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result := &BatchResult{
		SuccessSet:  make(map[string]bool),
		FailedIDs:   make(map[string]string),
		SKUToWooID:  make(map[string]int64),
	}

	// 🔥 ۳. پردازش Create آیتم‌ها (با تطبیق ایندکس)
	for i, created := range batchResp.Create {
		if i >= len(products) {
			break
		}
		prod := products[i]
		if created.Error != nil {
			result.FailedIDs[prod.SourceID] = created.Error.Message
		} else if created.ID > 0 {
			result.SuccessSet[prod.SourceID] = true
			result.SKUToWooID[prod.SourceID] = created.ID
		}
	}

	// 🔥 ۴. پردازش Update آیتم‌ها (با تطبیق ایندکس)
	// برای update، محصولات به همان ترتیب در products هستند
	// اما پاسخ ووکامرس برای update فقط شامل آیتم‌های موفق با ID است، خطاها درون Error می‌آیند
	// باید offset = len(batchResp.Create) را در نظر بگیریم
	offset := len(batchResp.Create)
	for i, updated := range batchResp.Update {
		idx := offset + i
		if idx >= len(products) {
			break
		}
		prod := products[idx]
		if updated.Error != nil {
			result.FailedIDs[prod.SourceID] = updated.Error.Message
		} else {
			result.SuccessSet[prod.SourceID] = true
			// برای update، نیازی به ذخیره WooID نیست (از قبل دارد)
		}
	}

	return result, nil
}
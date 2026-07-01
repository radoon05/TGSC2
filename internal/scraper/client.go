package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"tgsc/internal/config"
	"tgsc/internal/domain"
)

// Client handles scraping products from Eways with POST requests and pagination.
type Client struct {
	httpClient   *http.Client
	baseURL      string
	loginURL     string
	username     string
	password     string
	rateLimiter  *rate.Limiter
	retryMax     int
	retryBackoff time.Duration
	pageSize     int // تعداد محصول در هر صفحه (۲۴ در کد اصلی)
	categories   []config.Category
	loggedIn     bool
	mu           sync.Mutex
}

// NewClient creates a new scraper client with authentication and category support.

func NewClient(
	cfg *config.ScraperConfig,
	loginURL, username, password string,
	categories []config.Category,
) *Client {
	// دریافت کوکی با go-rod
	cookies, err := GetEwaysCookies(username, password, loginURL)
	if err != nil {
		log.Fatalf("❌ خطا در دریافت کوکی: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://panel.eways.co")
	jar.SetCookies(u, cookies)

	return &Client{
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
			Jar:     jar,
		},
		baseURL:      cfg.BaseURL,
		loginURL:     loginURL,
		username:     username,
		password:     password,
		rateLimiter:  rate.NewLimiter(rate.Limit(cfg.RateLimit), 1),
		retryMax:     cfg.RetryMax,
		retryBackoff: cfg.RetryBackoff,
		pageSize:     24,
		categories:   categories,
		loggedIn:     true,
	}
}

// login authenticates with Eways and stores session cookies.
func (c *Client) login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loggedIn {
		return nil
	}

	_ = c.rateLimiter.Wait(ctx)

	data := url.Values{}
	data.Set("username", c.username)
	data.Set("password", c.password)

	req, err := http.NewRequestWithContext(ctx, "POST", c.loginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// لاگین موفق با 302 یا 200
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return fmt.Errorf("login failed: %s", resp.Status)
	}
	c.loggedIn = true
	return nil
}

// FetchProducts scrapes all products from all categories and returns domain products.
func (c *Client) FetchProducts(ctx context.Context) ([]*domain.Product, error) {
	if err := c.login(ctx); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	var allProducts []*domain.Product

	for _, cat := range c.categories {
		if cat.EwaysCatID == "" {
			continue
		}
		products, err := c.fetchCategoryProducts(ctx, cat.EwaysCatID)
		if err != nil {
			// لاگ خطا ولی ادامه بده
			// می‌توانیم با logger لاگ کنیم، فعلاً ignore می‌کنیم
			continue
		}
		allProducts = append(allProducts, products...)
	}
	return allProducts, nil
}

// fetchCategoryProducts scrapes products for a single category with pagination.
func (c *Client) fetchCategoryProducts(ctx context.Context, catID string) ([]*domain.Product, error) {
	var allProducts []*domain.Product

	// صفحه‌بندی: MainPage و LazyPage (مثل کد اصلی)
	for mainPage := 0; mainPage <= 60; mainPage++ {
		foundOnThisMainPage := false

		for lazyPart := 0; lazyPart <= 5; lazyPart++ {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			// ساخت payload مثل کد اصلی
			payload := fmt.Sprintf(
				"ListViewType=0&CatId=%s&Order=2&Sort=2&LazyPageIndex=%d&PageIndex=%d&PageSize=24&Available=1&IsLazyLoading=true",
				catID, lazyPart, mainPage,
			)

			products, found, err := c.fetchPage(ctx, catID, payload)
			if err != nil {
				// اگر خطا داشت، لاگ کن و ادامه بده
				continue
			}
			if found {
				foundOnThisMainPage = true
				allProducts = append(allProducts, products...)
			} else {
				// اگر هیچ محصولی در این lazy part نبود، از حلقه lazy خارج شو
				break
			}
			// تأخیر بین درخواست‌ها مثل کد اصلی
			time.Sleep(200 * time.Millisecond)
		}
		// اگر در این mainPage هیچ محصولی پیدا نشد، صفحه‌بندی تمام شده
		if !foundOnThisMainPage {
			break
		}
	}
	return allProducts, nil
}

// fetchPage sends a single POST request and returns products.
func (c *Client) fetchPage(ctx context.Context, catID, payload string) ([]*domain.Product, bool, error) {
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, false, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewBufferString(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	// // لاگ کوکی‌ها
	// cookies := c.httpClient.Jar.Cookies(req.URL)
	// log.Printf("🔍 تعداد کوکی‌ها: %d", len(cookies))
	// for _, ck := range cookies {
	// 	log.Printf("   🍪 %s = %s...", ck.Name, ck.Value[:20])
	// }

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("status: %s", resp.Status)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}

	// Parse response
	var ewaysResp EwaysResponse
	if err := json.Unmarshal(bodyBytes, &ewaysResp); err != nil {
		return nil, false, fmt.Errorf("json decode: %w", err)
	}

	// داخل تابع fetchPage، بعد از Unmarshal:
	if len(ewaysResp.Goods) == 0 {
		return nil, false, nil
	}

	products := make([]*domain.Product, 0, len(ewaysResp.Goods))
	for _, g := range ewaysResp.Goods {
		// فقط محصولات موجود را ذخیره کنیم (اختیاری)
		// if !g.Availability { continue }

		products = append(products, &domain.Product{
			SourceID:      strconv.Itoa(g.ID),
			Title:         g.Name,
			Price:         g.Price,
			Stock:         g.Stock,
			LastScrapedAt: time.Now(),
		})
	}
	return products, true, nil

}

// Login را عمومی می‌کنیم تا از بیرون قابل دسترسی باشد
func (c *Client) Login(ctx context.Context) error {
	return c.login(ctx)
}

// FetchRaw یک درخواست POST با payload مشخص ارسال می‌کند و body خام را برمی‌گرداند
func (c *Client) FetchRaw(ctx context.Context, payload string) ([]byte, error) {
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewBufferString(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// EwaysResponse ساختار پاسخ دریافتی از API ایویز
type EwaysResponse struct {
	Goods []EwaysProduct `json:"Goods"`
}

// EwaysProduct ساختار دقیق یک محصول بر اساس کالبدشکافی JSON
type EwaysProduct struct {
	ID           int     `json:"Id"`           // شناسه محصول
	Name         string  `json:"Name"`         // عنوان محصول
	Price        float64 `json:"Price"`        // قیمت
	OldPrice     float64 `json:"OldPrice"`     // قیمت خط خورده (اختیاری)
	Stock        int     `json:"Stock"`        // موجودی
	Availability bool    `json:"Availability"` // وضعیت موجودی (true/false)
	ImageURL     string  `json:"ImageUrl"`     // آدرس تصویر
}

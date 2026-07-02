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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/time/rate"

	"tgsc/internal/config"
	"tgsc/internal/domain"
)

// ================================================================
//  Client
// ================================================================

type Client struct {
	httpClient   *http.Client
	baseURL      string
	loginURL     string
	username     string
	password     string
	rateLimiter  *rate.Limiter
	retryMax     int
	retryBackoff time.Duration
	pageSize     int
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

// ================================================================
//  لاگین
// ================================================================

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

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return fmt.Errorf("login failed: %s", resp.Status)
	}
	c.loggedIn = true
	return nil
}

// Login public method
func (c *Client) Login(ctx context.Context) error {
	return c.login(ctx)
}

// ================================================================
//  دریافت محصولات (اسکرپ اصلی)
// ================================================================

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
			continue
		}
		allProducts = append(allProducts, products...)
	}
	return allProducts, nil
}

func (c *Client) fetchCategoryProducts(ctx context.Context, catID string) ([]*domain.Product, error) {
	var allProducts []*domain.Product

	for mainPage := 0; mainPage <= 60; mainPage++ {
		foundOnThisMainPage := false
		for lazyPart := 0; lazyPart <= 5; lazyPart++ {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			payload := fmt.Sprintf(
				"ListViewType=0&CatId=%s&Order=2&Sort=2&LazyPageIndex=%d&PageIndex=%d&PageSize=24&Available=1&IsLazyLoading=true",
				catID, lazyPart, mainPage,
			)

			products, found, err := c.fetchPage(ctx, catID, payload)
			if err != nil {
				continue
			}
			if found {
				foundOnThisMainPage = true
				allProducts = append(allProducts, products...)
			} else {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
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

	var ewaysResp EwaysResponse
	if err := json.Unmarshal(bodyBytes, &ewaysResp); err != nil {
		return nil, false, fmt.Errorf("json decode: %w", err)
	}

	if len(ewaysResp.Goods) == 0 {
		return nil, false, nil
	}

	// پیدا کردن اطلاعات دسته‌بندی
	var wpCatID int
	var priceCoeff float64
	for _, cat := range c.categories {
		if cat.EwaysCatID == catID {
			wpCatID = cat.WPCatID
			priceCoeff = cat.PriceCoeff
			break
		}
	}
	if priceCoeff == 0 {
		priceCoeff = 1.0
	}

	products := make([]*domain.Product, 0, len(ewaysResp.Goods))
	for _, g := range ewaysResp.Goods {
		products = append(products, &domain.Product{
			SourceID:      strconv.Itoa(g.ID),
			Title:         g.Name,
			Price:         g.Price,
			Stock:         g.Stock,
			LastScrapedAt: time.Now(),
			WPCatID:       wpCatID,
			PriceCoeff:    priceCoeff,
			ImageURL:      g.ImageURL,
			EwaysCatID:    catID,
		})
	}
	return products, true, nil
}

// ================================================================
//  دریافت جزئیات کامل محصول (صفحه محصول)
// ================================================================

// EwaysProductDetail ساختار جزئیات کامل محصول از صفحه HTML
type EwaysProductDetail struct {
	ID          int      `json:"Id"`
	Name        string   `json:"Name"`
	Description string   `json:"Description"`
	Images      []string `json:"Images"`
	Attributes  []struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	} `json:"Attributes"`
}

// GetProductDetailFromPage با رفتن به صفحه محصول، اطلاعات را از HTML استخراج می‌کند
func (c *Client) GetProductDetailFromPage(ctx context.Context, productID string, categoryID string) (*EwaysProductDetail, error) {
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	// 🔥 آدرس صحیح محصول در ایویز
	url := fmt.Sprintf("https://panel.eways.co/Store/Detail/%s/%s", categoryID, productID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status: %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	detail := &EwaysProductDetail{
		ID:     parseInt(productID),
		Images: []string{},
		Attributes: []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		}{},
	}

	// ============================================================
	// ۱. توضیحات محصول (اگر وجود داشته باشد)
	// ============================================================
	// فعلاً خالی می‌گذاریم – می‌توانید selector واقعی را جایگزین کنید
	// مثال: doc.Find(".product-description").Text()
	detail.Description = ""

	// ============================================================
	// ۲. گالری تصاویر – استخراج از ویژگی onclick
	// ============================================================
	// ابتدا از تصویر اصلی (goods-image) تلاش می‌کنیم
	imgSelection := doc.Find(".goods-image img[onclick]")
	if imgSelection.Length() > 0 {
		onclick, exists := imgSelection.Attr("onclick")
		if exists {
			// استخراج آرایه تصاویر با regex
			re := regexp.MustCompile(`ShowPhotoGalleryDialog\(\[(.*?)\],`)
			matches := re.FindStringSubmatch(onclick)
			if len(matches) > 1 {
				// matches[1] شامل لیست URLها به صورت "url1","url2",...
				parts := strings.Split(matches[1], ",")
				for _, p := range parts {
					p = strings.Trim(p, "\" ")
					if p != "" {
						detail.Images = append(detail.Images, p)
					}
				}
			}
		}
	}

	// اگر تصویری پیدا نشد، از تام‌نیل‌ها استفاده کن
	if len(detail.Images) == 0 {
		doc.Find(".goods-thumb .thumbnail img").Each(func(i int, s *goquery.Selection) {
			if src, exists := s.Attr("src"); exists {
				detail.Images = append(detail.Images, src)
			}
		})
	}

	// ============================================================
	// ۳. ویژگی‌ها (مشخصات فنی) – از جدول
	// ============================================================
	doc.Find("#link1 .table tbody tr").Each(func(i int, s *goquery.Selection) {
		name := strings.TrimSpace(s.Find("td.text-nowrap.bold").Text())
		value := strings.TrimSpace(s.Find("td").Not(".text-nowrap.bold").Text())
		if name != "" && value != "" {
			detail.Attributes = append(detail.Attributes, struct {
				Name  string `json:"Name"`
				Value string `json:"Value"`
			}{Name: name, Value: value})
		}
	})

	return detail, nil
}

// helper: تبدیل string به int
func parseInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

// ================================================================
//  متدهای کمکی
// ================================================================

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

// ================================================================
//  ساختارهای پاسخ ایویز
// ================================================================

type EwaysResponse struct {
	Goods []EwaysProduct `json:"Goods"`
}

type EwaysProduct struct {
	ID           int     `json:"Id"`
	Name         string  `json:"Name"`
	Price        float64 `json:"Price"`
	OldPrice     float64 `json:"OldPrice"`
	Stock        int     `json:"Stock"`
	Availability bool    `json:"Availability"`
	ImageURL     string  `json:"ImageUrl"`
}

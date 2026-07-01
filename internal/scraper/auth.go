package scraper

import (
	"context"
	"net/http"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// GetEwaysCookies با استفاده از chromedp (headless=false) لاگین کرده و کوکی کامل را برمی‌گرداند
func GetEwaysCookies(username, password, loginURL string) ([]*http.Cookie, error) {
	// تنظیمات chromedp با headless=false (مرورگر قابل مشاهده)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false), // 🔴 مرورگر با GUI باز می‌شود
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("disable-notifications", true),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("window-size", "1200,800"),
	)

	// ایجاد context مرورگر
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var networkCookies []*network.Cookie

	err := chromedp.Run(ctx,
		// ۱. رفتن به صفحه لاگین
		chromedp.Navigate(loginURL),

		// ۲. منتظر ماندن برای ظاهر شدن فرم
		chromedp.WaitVisible(`#UserName`, chromedp.ByID),
		chromedp.Sleep(1*time.Second),

		// ۳. پر کردن فرم
		chromedp.SetValue(`#UserName`, username, chromedp.ByID),
		chromedp.SetValue(`#Password`, password, chromedp.ByID),
		chromedp.Sleep(500*time.Millisecond),

		// ۴. کلیک روی دکمه لاگین
		chromedp.EvaluateAsDevTools(`document.getElementById('btnSubmit').click();`, nil),

		// ۵. منتظر ماندن برای ریدایرکت و بارگذاری صفحه اصلی
		chromedp.Sleep(5*time.Second),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),

		// ۶. دریافت کوکی‌ها
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			networkCookies, err = network.GetCookies().Do(ctx)
			return err
		}),
	)

	if err != nil {
		return nil, err
	}

	// تبدیل []*network.Cookie به []*http.Cookie
	httpCookies := make([]*http.Cookie, 0, len(networkCookies))
	for _, c := range networkCookies {
		httpCookies = append(httpCookies, &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
			Expires:  time.Unix(int64(c.Expires), 0),
		})
	}

	return httpCookies, nil
}
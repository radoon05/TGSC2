package scraper

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"log"
)

// GetEwaysCookies با استفاده از chromedp لاگین کرده و کوکی کامل را برمی‌گرداند
// حالت headless با متغیر محیطی EWAYS_HEADLESS کنترل می‌شود (پیش‌فرض: true)
// در صورت بروز خطا، خطا برگردانده می‌شود (نه `log.Fatal`)
func GetEwaysCookies(username, password, loginURL string) ([]*http.Cookie, error) {
	// ============================================================
	// ۱. تنظیمات مرورگر
	// ============================================================
	headless := true
	if v := os.Getenv("EWAYS_HEADLESS"); v == "false" {
		headless = false
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var networkCookies []*network.Cookie
	var currentURL string

	log.Println("🤖 در حال شبیه‌سازی لاگین کاربر...")

	// ============================================================
	// ۲. اجرای فرآیند لاگین
	// ============================================================
	err := chromedp.Run(ctx,
		// رفتن به صفحه لاگین
		chromedp.Navigate(loginURL),

		// منتظر ماندن برای ظاهر شدن فرم
		chromedp.WaitVisible(`#UserName`, chromedp.ByID),
		chromedp.Sleep(1*time.Second),

		// پر کردن فرم
		chromedp.SetValue(`#UserName`, username, chromedp.ByID),
		chromedp.SetValue(`#Password`, password, chromedp.ByID),
		chromedp.Sleep(500*time.Millisecond),

		// کلیک روی دکمه لاگین
		chromedp.EvaluateAsDevTools(`document.getElementById('btnSubmit').click();`, nil),

		// منتظر ماندن برای ریدایرکت (صبر کافی)
		chromedp.Sleep(5*time.Second),

		// ============================================================
		// 🔥 ۳. راستی‌آزمایی لاگین (بررسی URL)
		// ============================================================
		chromedp.Location(&currentURL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			// اگر کاربر به صفحه لاگین برگشت، خطا بده
			if strings.Contains(currentURL, "Login") {
				return fmt.Errorf("لاگین ناموفق: کاربر همچنان در صفحه لاگین است (مشکل در نام کاربری/رمز یا کپچا)")
			}
			// (اختیاری) می‌توانید یک المان خاص پنل را هم چک کنید
			return nil
		}),

		// صبر اضافی برای اطمینان از نشستن کوکی‌ها
		chromedp.Sleep(2*time.Second),

		// ============================================================
		// ۴. دریافت کوکی‌ها
		// ============================================================
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			networkCookies, err = network.GetCookies().Do(ctx)
			return err
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("خطا در chromedp: %w", err)
	}

	log.Println("✅ کوکی‌های نشست با موفقیت دریافت شد.")

	// ============================================================
	// ۵. تبدیل []*network.Cookie به []*http.Cookie
	// ============================================================
	httpCookies := make([]*http.Cookie, 0, len(networkCookies))
	for _, c := range networkCookies {
		// مدیریت کوکی‌های session-only (Expires=-1 یا 0)
		var expires time.Time
		if c.Expires > 0 {
			expires = time.Unix(int64(c.Expires), 0)
		} // else: expires = زمان صفر (session cookie)

		httpCookies = append(httpCookies, &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
			Expires:  expires,
		})
	}
	return httpCookies, nil
}
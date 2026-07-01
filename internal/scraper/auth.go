package scraper

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// GetEwaysCookies با استفاده از HTTP Client ساده لاگین کرده و کوکی کامل (شامل Aut) را برمی‌گرداند
func GetEwaysCookies(username, password, loginURL string) ([]*http.Cookie, error) {
	// ایجاد CookieJar
	jar, _ := cookiejar.New(nil)

	// ساخت داده‌های فرم
	data := url.Values{}
	data.Set("username", username)
	data.Set("password", password)

	// ساخت درخواست
	req, err := http.NewRequest("POST", loginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,fa;q=0.8")
	req.Header.Set("Referer", "https://panel.eways.co/User/Login")

	// ✅ کلاینت بدون دنبال کردن ریدایرکت
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // جلوگیری از دنبال کردن ریدایرکت
		},
	}

	// اجرای درخواست
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// بررسی وضعیت (باید 302 باشد)
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("login failed with status: %d", resp.StatusCode)
	}

	// 🔑 استخراج کوکی Aut از هدر Set-Cookie
	var autCookie *http.Cookie
	for _, cookie := range resp.Header["Set-Cookie"] {
		// تجزیه کوکی
		parts := strings.Split(cookie, ";")
		nameValue := strings.TrimSpace(parts[0])
		nameValueParts := strings.SplitN(nameValue, "=", 2)
		if len(nameValueParts) != 2 {
			continue
		}
		name := nameValueParts[0]
		value := nameValueParts[1]

		if name == "Aut" {
			autCookie = &http.Cookie{
				Name:     name,
				Value:    value,
				Domain:   "panel.eways.co",
				Path:     "/",
				Secure:   true,
				HttpOnly: true,
				Expires:  time.Now().Add(24 * time.Hour), // تخمین زمان انقضا
			}
			break
		}
	}

	if autCookie == nil {
		return nil, fmt.Errorf("Aut cookie not found in login response")
	}

	// اضافه کردن کوکی Aut به Jar
	u, _ := url.Parse("https://panel.eways.co")
	jar.SetCookies(u, []*http.Cookie{autCookie})

	// همچنین کوکی config را هم از پاسخ بگیریم (اگر وجود داشت)
	for _, cookie := range resp.Header["Set-Cookie"] {
		parts := strings.Split(cookie, ";")
		nameValue := strings.TrimSpace(parts[0])
		nameValueParts := strings.SplitN(nameValue, "=", 2)
		if len(nameValueParts) != 2 {
			continue
		}
		name := nameValueParts[0]
		value := nameValueParts[1]

		if name == "config" {
			jar.SetCookies(u, []*http.Cookie{{
				Name:   name,
				Value:  value,
				Domain: "panel.eways.co",
				Path:   "/",
			}})
			break
		}
	}

	return jar.Cookies(u), nil
}
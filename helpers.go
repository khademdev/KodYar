// ابزارهای کمکی کوچک.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func timeNow() int64 { return time.Now().Unix() }

// randSuffix یک پسوند تصادفی hex برای شناسه‌ها.
func randSuffix(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// slugify تبدیل نام به شناسه‌ی مناسب.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "provider"
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

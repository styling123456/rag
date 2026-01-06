package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

func UrlEncode(s string) string { return url.QueryEscape(s) }

func EscapeLike(s string) string { return strings.ReplaceAll(s, "'", "''") }

func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " …"
}

func HashText(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

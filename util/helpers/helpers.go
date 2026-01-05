package helpers

import (
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

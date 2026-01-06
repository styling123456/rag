package rag

import (
	"fmt"
	"os"
)

func GetEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func GetEnvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		var i int
		fmt.Sscanf(v, "%d", &i)
		return i
	}
	return def
}

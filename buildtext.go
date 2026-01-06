package rag

import (
	"fmt"
	"strings"
)

func BuildText(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "[%s] %s %s %s rcode=%s", r.TS.Format("2006-01-02 15:04:05"), r.ClientIP, r.QName, r.QType, r.RCode)
		if r.UpInfo != "" {
			fmt.Fprintf(&b, " up=%s", r.UpInfo)
		}
		fmt.Fprintf(&b, " rt=%.0fms", r.LatencyMS)
		if r.Resv != "" {
			fmt.Fprintf(&b, " resv=%s", r.Resv)
		}
		if r.VSName != "" {
			fmt.Fprintf(&b, " vs=%s", r.VSName)
		}
		if r.DCName != "" {
			fmt.Fprintf(&b, " dc=%s", r.DCName)
		}
		b.WriteByte('\n')
	}
	s := b.String()
	// 控制块长度（示例：最多 100KB）
	const max = 100 * 1024
	if len(s) > max {
		return s[:max]
	}
	return s
}

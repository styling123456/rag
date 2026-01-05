// cmd/chunkjob/main.go
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"rag/util/embedder"
	"rag/util/env"
	"strings"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
)

type Row struct {
	TS        time.Time
	ClientIP  string
	QName     string
	QType     string
	RCode     string
	UpInfo    string
	LatencyMS float32
	Resv      string
	VSName    string
	DCName    string
}

var (
	chDsn     = env.GetEnv("CH_DSN", "clickhouse://default:MsTac%402001@192.168.15.44:9000/wsav3")
	winBefore = flag.Duration("win-before", 5*time.Minute, "window before")
	winAfter  = flag.Duration("win-after", 5*time.Minute, "window after")
	fromStr   = flag.String("from", "2026-01-05 14:40:00", "t_start, e.g. 2025-12-31 17:40:00")
	toStr     = flag.String("to", "2026-01-05 14:50:00", "t_end, e.g. 2025-12-31 17:46:00")
	qlike     = flag.String("qname-like", "%", "like filter, e.g. %smart.xyz%")
)

func main() {
	flag.Parse()
	if *fromStr == "" || *toStr == "" {
		log.Fatal("--from and --to are required")
	}

	t1, err := time.ParseInLocation("2006-01-02 15:04:05", *fromStr, time.Local)
	if err != nil {
		log.Fatal(err)
	}
	t2, err := time.ParseInLocation("2006-01-02 15:04:05", *toStr, time.Local)
	if err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("clickhouse", chDsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 1) 拉明细 → 组装文本块
	title := fmt.Sprintf("DNS窗口: %s ~ %s", t1.Format(time.RFC3339), t2.Format(time.RFC3339))
	rows, err := fetchRows(db, t1, t2, *qlike)
	if err != nil {
		log.Fatal(err)
	}
	if len(rows) == 0 {
		log.Println("no rows in window")
		return
	}

	text := buildText(rows)

	// 2) 调 /embed
	vec, err := embedder.Embed(text)
	if err != nil {
		log.Fatal(err)
	}

	// 3) INSERT ai_chunks
	if err := insertChunk(db, t1.Add(-*winBefore), t2.Add(*winAfter), title, text, vec); err != nil {
		log.Fatal(err)
	}

	log.Printf("ok: inserted chunk for window %s ~ %s, len(text)=%d", t1, t2, len(text))
}

func fetchRows(db *sql.DB, t1, t2 time.Time, like string) ([]Row, error) {
	const q = `
SELECT ts, client_ip_raw, qname, qtype, rcode, upstream_info, latency_ms, resolve_info, response_vs_name, response_dc_name
FROM events_dns_v
WHERE ts BETWEEN ? AND ?
  AND qname_root ILIKE ?
ORDER BY ts
LIMIT 2000`
	ctx := context.Background()
	rs, err := db.QueryContext(ctx, q, t1, t2, like)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := make([]Row, 0, 256)
	for rs.Next() {
		var r Row
		if err := rs.Scan(&r.TS, &r.ClientIP, &r.QName, &r.QType, &r.RCode, &r.UpInfo, &r.LatencyMS, &r.Resv, &r.VSName, &r.DCName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func buildText(rows []Row) string {
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

func insertChunk(db *sql.DB, wStart, wEnd time.Time, title, text string, vec []float32) error {
	const ins = `INSERT INTO ai_chunks (id, source_type, source_ref, window_start, window_end, title, text, embedding, idx_date)
                 VALUES (generateUUIDv4(), 'dns_event', ?, ?, ?, ?, ?, ?, toDate(?))`
	_, err := db.Exec(ins, fmt.Sprintf("win=%s_%s", wStart, wEnd), wStart, wEnd, title, text, vec, wStart)
	return err
}

// cmd/chunkjob/main.go
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"rag"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
)

var (
	chDsn     = rag.GetEnv("CH_DSN", "clickhouse://default:MsTac%402001@192.168.15.44:9000/wsav3")
	winBefore = flag.Duration("win-before", 5*time.Minute, "window before")
	winAfter  = flag.Duration("win-after", 5*time.Minute, "window after")
	fromStr   = flag.String("from", "2026-01-05 17:40:00", "t_start, e.g. 2025-12-31 17:40:00")
	toStr     = flag.String("to", "2026-01-05 17:50:00", "t_end, e.g. 2025-12-31 17:46:00")
	qlike     = flag.String("qname-like", "%", "like filter, e.g. %smart.xyz%")
	rcode     = flag.String("rcode", "", "response code , e.g. NOERROR, NXDOMAIN, SERVFAIL")
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
	rows, err := rag.FetchRows(db, t1, t2, *qlike, *rcode)
	if err != nil {
		log.Fatal(err)
	}
	if len(rows) == 0 {
		log.Println("no rows in window")
		return
	}

	text := rag.BuildText(rows)

	// 2) 调 /embed
	vec, err := rag.Embed(text)
	if err != nil {
		log.Fatal(err)
	}

	// 3) INSERT ai_chunks
	ref := fmt.Sprintf("win=%s_%s", t1, t2)
	if _, err = rag.InsertChunk(db, t1.Add(-*winBefore), t2.Add(*winAfter), title, text, vec, ref, ""); err != nil {
		log.Fatal(err)
	}

	log.Printf("ok: inserted chunk for window %s ~ %s, len(text)=%d", t1, t2, len(text))
}

// cmd/chunk-svc/main.go
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	_ "os"
	"rag/util/embedder"
	"rag/util/env"
	"strings"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
)

type buildReq struct {
	TStart       string `json:"t_start"` // "YYYY-MM-DD HH:MM:SS"
	TEnd         string `json:"t_end"`
	QNameLike    string `json:"qname_like"` // "%smart.xyz%"
	WinBeforeSec int    `json:"win_before_sec"`
	WinAfterSec  int    `json:"win_after_sec"`
}

type buildResp struct {
	ChunkID     string `json:"chunk_id"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	Title       string `json:"title"`
	Lines       int    `json:"lines"`
	Inserted    bool   `json:"inserted"`
	Note        string `json:"note"`
}

type row struct {
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
	chDsn  = env.GetEnv("CH_DSN", "clickhouse://default:MsTac%402001@192.168.15.44:9000/wsav3")
	embDim = env.GetEnvInt("EMB_DIM", 512)
)

func main() {
	db, err := sql.Open("clickhouse", chDsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/chunks/build", func(w http.ResponseWriter, r *http.Request) {
		var req buildReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if req.QNameLike == "" {
			req.QNameLike = "%"
		}
		if req.WinBeforeSec == 0 {
			req.WinBeforeSec = 300
		}
		if req.WinAfterSec == 0 {
			req.WinAfterSec = 300
		}

		t1, err := time.ParseInLocation("2006-01-02 15:04:05", req.TStart, time.Local)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "bad t_start"})
			return
		}
		t2, err := time.ParseInLocation("2006-01-02 15:04:05", req.TEnd, time.Local)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "bad t_end"})
			return
		}

		rows, err := fetchRows(db, t1, t2, req.QNameLike)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		if len(rows) == 0 {
			writeJSON(w, 200, buildResp{Inserted: false, Note: "no rows in window"})
			return
		}

		title := fmt.Sprintf("DNS窗口: %s ~ %s 事件数=%d", t1.Format(time.RFC3339), t2.Format(time.RFC3339), len(rows))
		text := buildText(rows)
		wStart := t1.Add(-time.Duration(req.WinBeforeSec) * time.Second)
		wEnd := t2.Add(time.Duration(req.WinAfterSec) * time.Second)
		sourceRef := fmt.Sprintf("win=%s_%s", wStart, wEnd)
		hash := hashText(title + "\n" + text)

		have, err := existsByHash(db, hash)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		if have {
			writeJSON(w, 200, buildResp{ChunkID: "", WindowStart: wStart.Format(time.RFC3339), WindowEnd: wEnd.Format(time.RFC3339), Title: title, Lines: len(rows), Inserted: false, Note: "duplicate by hash"})
			return
		}

		vec, err := embedder.Embed(text)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}

		id, err := insertChunk(db, wStart, wEnd, title, text, vec, sourceRef, hash)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}

		writeJSON(w, 200, buildResp{ChunkID: id, WindowStart: wStart.Format(time.RFC3339), WindowEnd: wEnd.Format(time.RFC3339), Title: title, Lines: len(rows), Inserted: true})
	})

	log.Println("chunk-svc on :8089")
	log.Fatal(http.ListenAndServe(":8089", mux))
}

func fetchRows(db *sql.DB, t1, t2 time.Time, like string) ([]row, error) {
	const q = `
SELECT ts, client_ip_raw, qname, qtype, rcode, upstream_info, latency_ms, resolve_info, response_vs_name, response_dc_name
FROM events_dns_v
WHERE ts BETWEEN ? AND ? AND qname_root ILIKE ?
ORDER BY ts LIMIT 2000`
	rs, err := db.QueryContext(context.Background(), q, t1, t2, like)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := make([]row, 0, 256)
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.TS, &r.ClientIP, &r.QName, &r.QType, &r.RCode, &r.UpInfo, &r.LatencyMS, &r.Resv, &r.VSName, &r.DCName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func buildText(rows []row) string {
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
	if len(s) > 100*1024 {
		return s[:100*1024]
	}
	return s
}

func insertChunk(db *sql.DB, ws, we time.Time, title, text string, vec []float32, ref, hash string) (string, error) {
	const ins = `
INSERT INTO ai_chunks (id, source_type, source_ref, window_start, window_end, title, text, embedding, tokens, idx_date)
VALUES (generateUUIDv4(), 'dns_event', ?, ?, ?, ?, ?, ?, lengthUTF8(?), toDate(?)) RETURNING id`
	var id string
	err := db.QueryRow(ins, ref, ws, we, title, text, vec, text, ws).Scan(&id)
	return id, err
}

func existsByHash(db *sql.DB, h string) (bool, error) {
	// 若 ai_chunks 未建 hash 列，这里用 cityHash64(text) 近似去重
	const q = `SELECT count() FROM ai_chunks WHERE cityHash64(text) = cityHash64(?) LIMIT 1`
	var c int
	if err := db.QueryRow(q, h).Scan(&c); err != nil {
		return false, err
	}
	return c > 0, nil
}

func hashText(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ---- helpers ----
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

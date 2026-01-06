// cmd/chunk-svc/main.go
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	_ "os"
	"rag"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
)

var (
	chDsn  = rag.GetEnv("CH_DSN", "clickhouse://default:MsTac%402001@192.168.15.44:9000/wsav3")
	embDim = rag.GetEnvInt("EMB_DIM", 512)
)

type buildReq struct {
	TStart       string `json:"t_start"` // "YYYY-MM-DD HH:MM:SS"
	TEnd         string `json:"t_end"`
	QNameLike    string `json:"qname_like"` // "%smart.xyz%"
	WinBeforeSec int    `json:"win_before_sec"`
	WinAfterSec  int    `json:"win_after_sec"`
	RCode        string `json:"rcode"` // 新增：可传 "NXDOMAIN" / "SERVFAIL" / ""(全部)
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

		rows, err := rag.FetchRows(db, t1, t2, req.QNameLike, req.RCode)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		if len(rows) == 0 {
			writeJSON(w, 200, buildResp{Inserted: false, Note: "no rows in window"})
			return
		}

		title := fmt.Sprintf("DNS窗口: %s ~ %s 事件数=%d", t1.Format(time.RFC3339), t2.Format(time.RFC3339), len(rows))
		text := rag.BuildText(rows)
		wStart := t1.Add(-time.Duration(req.WinBeforeSec) * time.Second)
		wEnd := t2.Add(time.Duration(req.WinAfterSec) * time.Second)
		sourceRef := fmt.Sprintf("win=%s_%s", wStart, wEnd)
		hash := rag.HashText(title + "\n" + text)

		have, err := rag.ExistsByHash(db, hash)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		if have {
			writeJSON(w, 200, buildResp{ChunkID: "", WindowStart: wStart.Format(time.RFC3339), WindowEnd: wEnd.Format(time.RFC3339), Title: title, Lines: len(rows), Inserted: false, Note: "duplicate by hash"})
			return
		}

		vec, err := rag.Embed(text)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}

		id, err := rag.InsertChunk(db, wStart, wEnd, title, text, vec, sourceRef, hash)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}

		writeJSON(w, 200, buildResp{ChunkID: id, WindowStart: wStart.Format(time.RFC3339), WindowEnd: wEnd.Format(time.RFC3339), Title: title, Lines: len(rows), Inserted: true})
	})

	log.Println("chunk-svc on :8089")
	log.Fatal(http.ListenAndServe(":8089", mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// cmd/rag-api/main.go
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"rag"
	"strings"

	_ "github.com/ClickHouse/clickhouse-go/v2"
)

type AskReq struct {
	Question string   `json:"question"`
	Filters  []string `json:"filters"` // 可选：qname/client_ip等
	TopK     int      `json:"topk"`
}

type Chunk struct {
	ID       string
	Title    string
	Text     string
	WinStart string
	WinEnd   string
}

func main() {
	chDsn := rag.GetEnv("CH_DSN", "clickhouse://default:MsTac%402001@192.168.15.44:9000/wsav3")

	db, err := sql.Open("clickhouse", chDsn)
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/ask", func(w http.ResponseWriter, r *http.Request) {
		var req AskReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.TopK == 0 {
			req.TopK = 6
		}

		// 1) 调 embedder 得到 query 向量
		qvec, err := rag.Embed(req.Question)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// 2) ClickHouse TopK 检索（cosineDistance）+ 关键词过滤（简化）
		// 注意：使用参数化避免 SQL 注入
		q := fmt.Sprintf(`
            SELECT id, title, text, toString(window_start), toString(window_end)
            FROM ai_chunks
            %s
            ORDER BY cosineDistance(embedding, ?) ASC
            LIMIT %d`, whereLike(req.Filters), req.TopK)

		rows, err := db.QueryContext(context.Background(), q, qvec)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()

		var chunks []Chunk
		for rows.Next() {
			var c Chunk
			if err := rows.Scan(&c.ID, &c.Title, &c.Text, &c.WinStart, &c.WinEnd); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			chunks = append(chunks, c)
		}

		// 3) 生成可复现链接（示例：把窗口 SQL encode 入 URL）
		var links []string
		for _, c := range chunks {
			sqlURL := fmt.Sprintf("/explore?sql=%s", rag.UrlEncode(fmt.Sprintf(
				"SELECT * FROM events_dns_v WHERE ts BETWEEN parseDateTimeBestEffort('%s') AND parseDateTimeBestEffort('%s') LIMIT 200",
				c.WinStart, c.WinEnd,
			)))
			links = append(links, sqlURL)
		}

		// 4) 调 /generate 组装草案
		genBody := map[string]any{
			"system":    "你是网络/安全运维助手，输出需包含‘证据与链接’。",
			"prompt":    req.Question,
			"evidences": topTexts(chunks, 4),
		}
		draft, err := rag.Generate(genBody)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// TODO: INSERT ai_queries_log（略）

		resp := map[string]any{
			"answer": draft,
			"chunks": chunks,
			"links":  links,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	log.Println("RAG API on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func topTexts(chs []Chunk, n int) []string {
	if n > len(chs) {
		n = len(chs)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s\n%s", chs[i].Title, rag.Truncate(chs[i].Text, 800)))
	}
	return out
}

func whereLike(filters []string) string {
	if len(filters) == 0 {
		return ""
	}
	// 简化：对 qname/文本 LIKE 过滤
	clauses := make([]string, 0, len(filters))
	for _, f := range filters {
		clauses = append(clauses, fmt.Sprintf("text ILIKE '%%%s%%'", rag.EscapeLike(f)))
	}
	return "WHERE " + strings.Join(clauses, " AND ")
}

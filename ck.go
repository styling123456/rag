package rag

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
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

func FetchRows(db *sql.DB, t1, t2 time.Time, like, rcode string) ([]Row, error) {
	q := `
SELECT ts, client_ip_raw, qname, qtype, rcode, upstream_info, latency_ms, resolve_info, response_vs_name, response_dc_name
FROM events_dns_v
WHERE ts BETWEEN ? AND ? AND qname_root ILIKE ?`
	args := []any{t1, t2, like}
	if rcode != "" {
		q += " AND rcode = ?"
		args = append(args, rcode)
	}
	q += " ORDER BY ts LIMIT 2000"
	rs, err := db.QueryContext(context.Background(), q, args...)
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

func InsertChunk(db *sql.DB, ws, we time.Time, title, text string, vec []float32, ref, hash string) (string, error) {
	const ins = `
INSERT INTO ai_chunks (id, source_type, source_ref, window_start, window_end, title, text, embedding, tokens, idx_date)
VALUES (?, 'dns_event', ?, ?, ?, ?, ?, ?, lengthUTF8(?), toDate(?))`
	id := uuid.NewString()
	_, err := db.Exec(ins, id, ref, ws, we, title, text, vec, text, ws)
	return id, err
}

func ExistsByHash(db *sql.DB, h string) (bool, error) {
	// 若 ai_chunks 未建 hash 列，这里用 cityHash64(text) 近似去重
	const q = `SELECT count() FROM ai_chunks WHERE cityHash64(text) = cityHash64(?) LIMIT 1`
	var c int
	if err := db.QueryRow(q, h).Scan(&c); err != nil {
		return false, err
	}
	return c > 0, nil
}

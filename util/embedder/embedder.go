package embedder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"rag/util/env"
)

var (
	embURL = env.GetEnv("EMBED_URL", "http://192.168.12.181:8081/embed")
	genURL = env.GetEnv("GEN_URL", "http://192.168.12.181:8081/generate")
)

func Embed(text string) ([]float32, error) {
	body := map[string]any{"texts": []string{text}}
	b, _ := json.Marshal(body)
	resp, err := http.Post(embURL, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Vectors [][]float32 `json:"vectors"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Vectors) == 0 || len(out.Vectors[0]) == 0 {
		return nil, fmt.Errorf("empty vectors")
	}
	// 容错：如果维度不匹配，截断/补零
	v := out.Vectors[0]
	//if len(v) > embDim {
	//	v = v[:embDim]
	//}
	//if len(v) < embDim {
	//	v = append(v, make([]float32, embDim-len(v))...)
	//}
	return v, nil
}

func Generate(body map[string]any) (string, error) {
	b, _ := json.Marshal(body)
	resp, err := http.Post(genURL, "application/json", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Text string `json:"text"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Text, nil
}

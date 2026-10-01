// Package swebench は SWE-bench のインスタンスを取得し、正解ファイルを取り出す。
package swebench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// BaseURL は HuggingFace の行取得 API の基点。テストでは httptest の URL へ差し替える。
var BaseURL = "https://datasets-server.huggingface.co"

// HTTPClient は取得に使う HTTP クライアント。
var HTTPClient = &http.Client{Timeout: 60 * time.Second}

// maxRowsPerCall は行取得 API の length 上限。
const maxRowsPerCall = 100

// Instance は SWE-bench の 1 インスタンス。v0 で使うものだけ持つ。
type Instance struct {
	Repo             string
	InstanceID       string
	BaseCommit       string
	Patch            string
	ProblemStatement string
}

type rowsResponse struct {
	NumRowsTotal int `json:"num_rows_total"`
	Rows         []struct {
		RowIdx int `json:"row_idx"`
		Row    struct {
			Repo             string `json:"repo"`
			InstanceID       string `json:"instance_id"`
			BaseCommit       string `json:"base_commit"`
			Patch            string `json:"patch"`
			ProblemStatement string `json:"problem_statement"`
		} `json:"row"`
	} `json:"rows"`
}

// Load は dataset の test split を offset を進めながら取得する。
// limit が 0 以下のときは全件を取る。
func Load(ctx context.Context, dataset string, limit int) ([]Instance, error) {
	var out []Instance
	for offset := 0; ; {
		length := maxRowsPerCall
		if limit > 0 {
			remain := limit - len(out)
			if remain <= 0 {
				break
			}
			if remain < length {
				length = remain
			}
		}

		page, total, err := fetchRows(ctx, dataset, offset, length)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		out = append(out, page...)
		offset += len(page)
		// API が length を越えて返してきても、limit は守る。
		if limit > 0 && len(out) >= limit {
			out = out[:limit]
			break
		}

		if total > 0 && offset >= total {
			break
		}
	}
	return out, nil
}

func fetchRows(ctx context.Context, dataset string, offset, length int) ([]Instance, int, error) {
	u := fmt.Sprintf("%s/rows?dataset=%s&config=default&split=test&offset=%d&length=%d",
		BaseURL, url.QueryEscape(dataset), offset, length)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("swebench: 行取得 API が %s を返した (offset=%d)", resp.Status, offset)
	}

	var body rowsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, fmt.Errorf("swebench: 応答を解釈できない: %w", err)
	}

	out := make([]Instance, 0, len(body.Rows))
	for _, r := range body.Rows {
		out = append(out, Instance{
			Repo:             r.Row.Repo,
			InstanceID:       r.Row.InstanceID,
			BaseCommit:       r.Row.BaseCommit,
			Patch:            r.Row.Patch,
			ProblemStatement: r.Row.ProblemStatement,
		})
	}
	return out, body.NumRowsTotal, nil
}

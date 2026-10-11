// Package mcpserver adapts the public REST API to MCP. It never opens the database.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"inventrospect/internal/inventory"
)

type Backend struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewBackend(base, token string) (*Backend, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("base URL must be an http(s) application URL without credentials, query or fragment")
	}
	return &Backend{BaseURL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (b *Backend) request(ctx context.Context, method, path, typ string, body io.Reader) ([]byte, error) {
	if !strings.HasPrefix(b.Token, "inv_") {
		return nil, inventory.ErrAuth
	}
	r, e := http.NewRequestWithContext(ctx, method, b.BaseURL+"/api/v1"+path, body)
	if e != nil {
		return nil, fmt.Errorf("cannot construct inventory request")
	}
	r.Header.Set("Authorization", "Bearer "+b.Token)
	if typ != "" {
		r.Header.Set("Content-Type", typ)
	}
	res, e := b.HTTP.Do(r)
	if e != nil {
		return nil, &inventory.Error{Code: "unavailable", Message: "Inventory API unavailable; do not automatically retry mutations"}
	}
	defer res.Body.Close()
	data, e := io.ReadAll(io.LimitReader(res.Body, 40*1024*1024+1))
	if e != nil {
		return nil, &inventory.Error{Code: "unavailable", Message: "Incomplete inventory response; inspect state before retrying mutations"}
	}
	if len(data) > 40*1024*1024 {
		return nil, &inventory.Error{Code: "too_large", Message: "Inventory response exceeds 40 MiB"}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var out struct {
			Error *inventory.Error `json:"error"`
		}
		if json.Unmarshal(data, &out) == nil && out.Error != nil {
			return nil, out.Error
		}
		return nil, &inventory.Error{Code: "upstream_error", Message: fmt.Sprintf("Inventory API returned HTTP %d", res.StatusCode)}
	}
	return data, nil
}
func (b *Backend) JSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(data)
	}
	data, e := b.request(ctx, method, path, "application/json", body)
	if e != nil {
		return e
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if e = json.Unmarshal(data, &envelope); e != nil {
		return &inventory.Error{Code: "upstream_error", Message: "Invalid inventory JSON response"}
	}
	if output != nil {
		return json.Unmarshal(envelope.Data, output)
	}
	return nil
}
func (b *Backend) Check(ctx context.Context) error { return b.JSON(ctx, "GET", "/agent/me", nil, nil) }

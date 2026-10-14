package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"inventrospect/internal/inventory"
	"inventrospect/internal/web"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func localClient(h http.Handler) *http.Client {
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Result(), nil
	})}
}

type fixture struct {
	s        *inventory.Service
	api      http.Handler
	b        *Backend
	admin    inventory.Actor
	token    inventory.TokenCreated
	item     inventory.Item
	from, to inventory.Node
}

func setup(t *testing.T, scopes ...string) *fixture {
	t.Helper()
	s, e := inventory.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if e = s.Bootstrap(ctx, "admin", "test-admin-password"); e != nil {
		t.Fatal(e)
	}
	a := inventory.Actor{UserID: 1}
	i, e := s.SaveItem(ctx, a, inventory.Item{Name: "MOSFET 60V", Manufacturer: "Example", MPN: "F60", Attributes: map[string]string{"Vds": "60 V"}})
	if e != nil {
		t.Fatal(e)
	}
	src, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Drawer A17"})
	if e != nil {
		t.Fatal(e)
	}
	dst, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Box B4"})
	if e != nil {
		t.Fatal(e)
	}
	token, e := s.CreateToken(ctx, a, inventory.TokenInput{Name: "Test MCP", UserID: 1, Scopes: scopes})
	if e != nil {
		t.Fatal(e)
	}
	h := web.New(s, false)
	b, e := NewBackend("http://inventory.test", token.Secret)
	if e != nil {
		t.Fatal(e)
	}
	b.HTTP = localClient(h)
	return &fixture{s: s, api: h, b: b, admin: a, token: token, item: i, from: src, to: dst}
}
func connect(t *testing.T, b *Backend) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, e := New(b).Connect(ctx, st, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ss.Close() })
	cs, e := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil).Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}
func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	r, e := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		t.Fatal(name, e)
	}
	if r.IsError {
		t.Fatalf("%s failed: %+v", name, r.Content)
	}
	return r
}
func resultJSON(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	b, e := json.Marshal(r.StructuredContent)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestMCPDiscoverySearchItemAndResources(t *testing.T) {
	f := setup(t, inventory.TokenScopes...)
	cs := connect(t, f.b)
	tools, e := cs.ListTools(context.Background(), nil)
	if e != nil || len(tools.Tools) != 27 {
		t.Fatal(tools, e)
	}
	for _, tool := range tools.Tools {
		if strings.Contains(tool.Name, "sql") {
			t.Fatal("destructive tool", tool.Name)
		}
		if tool.Description == "" {
			t.Fatal("missing description")
		}
	}
	r := call(t, cs, "inventory_search", map[string]any{"query": "mosfet", "limit": 1})
	if !strings.Contains(resultJSON(t, r), "MOSFET 60V") || strings.Contains(resultJSON(t, r), "Vds") {
		t.Fatal(resultJSON(t, r))
	}
	r = call(t, cs, "inventory_get_item", map[string]any{"id": f.item.ID})
	if !strings.Contains(resultJSON(t, r), `"Vds":"60 V"`) || !strings.Contains(resultJSON(t, r), `"stock"`) {
		t.Fatal(resultJSON(t, r))
	}
	r = call(t, cs, "inventory_list_locations", map[string]any{"limit": 1})
	if !strings.Contains(resultJSON(t, r), `"next_offset":1`) {
		t.Fatal(resultJSON(t, r))
	}
	resources, e := cs.ListResourceTemplates(context.Background(), nil)
	if e != nil || len(resources.ResourceTemplates) != 4 {
		t.Fatal(resources, e)
	}
	for _, uri := range []string{"inventory://items/1", "inventory://items/1/documents", "inventory://locations/1"} {
		res, e := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
		if e != nil || len(res.Contents) != 1 {
			t.Fatal(uri, res, e)
		}
	}
	for _, args := range []map[string]any{{"id": "one"}, {"id": 0}, {"id": 1, "unknown": true}, {}} {
		r, e := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "inventory_get_item", Arguments: args})
		if e == nil && !r.IsError {
			t.Fatal("malformed input accepted", args)
		}
	}
	if _, e = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "inventory_run_sql", Arguments: map[string]any{"id": 1}}); e == nil {
		t.Fatal("unknown tool accepted")
	}
	for _, uri := range []string{"file:///etc/passwd", "inventory://documents/../../etc/passwd", "inventory://documents/1?path=/etc/passwd"} {
		if _, e = cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri}); e == nil {
			t.Fatal("unsafe URI accepted", uri)
		}
	}
}
func TestMCPStockAndBulk(t *testing.T) {
	f := setup(t, inventory.TokenScopes...)
	cs := connect(t, f.b)
	call(t, cs, "inventory_adjust_stock", map[string]any{"id": 1, "location_id": f.from.ID, "quantity": 10})
	call(t, cs, "inventory_move_stock", map[string]any{"id": 1, "from_location_id": f.from.ID, "to_location_id": f.to.ID, "quantity": 3})
	r := call(t, cs, "inventory_get_stock", map[string]any{"id": 1})
	if !strings.Contains(resultJSON(t, r), `"total":10`) {
		t.Fatal(resultJSON(t, r))
	}
	stock, e := f.s.Stock(context.Background(), f.admin, 1)
	if e != nil || len(stock.History) != 3 || stock.History[0].TokenID == nil {
		t.Fatal(stock, e)
	}
	r = call(t, cs, "inventory_get_stock_history", map[string]any{"id": 1, "limit": 1})
	if !strings.Contains(resultJSON(t, r), `"next_before_id":3`) {
		t.Fatal(resultJSON(t, r))
	}
	r = call(t, cs, "inventory_create_items_bulk", map[string]any{"records": []any{map[string]any{"item": map[string]any{"name": "Bearing"}}, map[string]any{"item": map[string]any{"name": ""}}}})
	if !strings.Contains(resultJSON(t, r), `"succeeded":1`) || !strings.Contains(resultJSON(t, r), `"failed":1`) {
		t.Fatal(resultJSON(t, r))
	}
	r = call(t, cs, "inventory_update_items_bulk", map[string]any{"records": []any{map[string]any{"id": 2, "expected_revision": 1, "item": map[string]any{"name": "Updated bearing"}}, map[string]any{"id": 999, "expected_revision": 1, "item": map[string]any{"name": "Missing"}}}})
	if !strings.Contains(resultJSON(t, r), `"failed":1`) {
		t.Fatal(resultJSON(t, r))
	}
	r = call(t, cs, "inventory_adjust_stock_bulk", map[string]any{"records": []any{map[string]any{"item_id": 1, "adjustment": map[string]any{"location_id": f.from.ID, "quantity": 2}}, map[string]any{"item_id": 1, "adjustment": map[string]any{"location_id": f.from.ID, "quantity": -100}}}})
	if !strings.Contains(resultJSON(t, r), `"succeeded":1`) {
		t.Fatal(resultJSON(t, r))
	}
	bad, e := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "inventory_adjust_stock", Arguments: map[string]any{"id": 1, "location_id": 1, "quantity": 0.5}})
	if e == nil && !bad.IsError {
		t.Fatal("fractional mutation accepted")
	}
	call(t, cs, "inventory_archive_item", map[string]any{"id": 1, "expected_revision": 1, "archived": true})
	bad, e = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "inventory_adjust_stock", Arguments: map[string]any{"id": 1, "location_id": 1, "quantity": 1}})
	if e == nil && !bad.IsError {
		t.Fatal("archived mutation accepted")
	}
}
func TestMCPDocumentBytesAndPermissions(t *testing.T) {
	f := setup(t, inventory.TokenScopes...)
	cs := connect(t, f.b)
	pdf := []byte("%PDF-1.7\n test datasheet bytes\n%%EOF")
	call(t, cs, "document_attach", map[string]any{"item_id": 1, "filename": "../../datasheet.pdf", "type": "datasheet", "content_base64": base64.StdEncoding.EncodeToString(pdf)})
	call(t, cs, "document_list", map[string]any{"id": 1})
	r := call(t, cs, "document_get", map[string]any{"id": 1})
	var embedded *mcp.EmbeddedResource
	for _, c := range r.Content {
		if v, ok := c.(*mcp.EmbeddedResource); ok {
			embedded = v
		}
	}
	if embedded == nil || !bytes.Equal(embedded.Resource.Blob, pdf) || embedded.Resource.MIMEType != "application/pdf" {
		t.Fatalf("wrong binary content: %+v", embedded)
	}
	res, e := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "inventory://documents/1"})
	if e != nil || !bytes.Equal(res.Contents[0].Blob, pdf) {
		t.Fatal(res, e)
	}
	bad, e := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "document_attach", Arguments: map[string]any{"item_id": 1, "filename": "bad", "type": "other", "content_base64": "not base64"}})
	if e == nil && !bad.IsError {
		t.Fatal("invalid base64 accepted")
	}
	narrow, e := f.s.CreateToken(context.Background(), f.admin, inventory.TokenInput{Name: "inventory only", UserID: 1, Scopes: []string{"inventory:read"}})
	if e != nil {
		t.Fatal(e)
	}
	b := *f.b
	b.Token = narrow.Secret
	reader := connect(t, &b)
	r = call(t, reader, "inventory_get_item", map[string]any{"id": 1})
	if !strings.Contains(resultJSON(t, r), `"omitted_scopes":["stock:read","documents:read"]`) {
		t.Fatal(resultJSON(t, r))
	}
	for _, name := range []string{"document_get", "inventory_get_stock", "inventory_archive_item"} {
		args := map[string]any{"id": 1}
		if name == "inventory_archive_item" {
			args["archived"] = true
			args["expected_revision"] = 1
		}
		bad, e = reader.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil || !bad.IsError || !strings.Contains(resultJSON(t, bad), "forbidden") {
			t.Fatal(name, bad, e)
		}
	}
	if _, e = reader.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "inventory://documents/1"}); e == nil {
		t.Fatal("resource scope bypass")
	}
	if e = f.s.RevokeToken(context.Background(), f.admin, narrow.Token.ID); e != nil {
		t.Fatal(e)
	}
	bad, e = reader.CallTool(context.Background(), &mcp.CallToolParams{Name: "inventory_search", Arguments: map[string]any{}})
	if e != nil || !bad.IsError || !strings.Contains(resultJSON(t, bad), "unauthorized") {
		t.Fatal(bad, e)
	}
	b.Token = "inv_" + strings.Repeat("0", 64)
	if e = b.Check(context.Background()); e == nil {
		t.Fatal("invalid token accepted")
	}
}
func TestStreamableHTTPAuthenticationAndProtocol(t *testing.T) {
	f := setup(t, inventory.TokenScopes...)
	handler := httpHandler("http://inventory.test", f.b.HTTP)
	for _, tc := range []struct {
		token, origin string
		status        int
	}{{"", "", 401}, {"inv_" + strings.Repeat("0", 64), "", 401}, {f.token.Secret, "https://evil.test", 403}} {
		r := httptest.NewRequest("POST", "http://mcp.test/mcp", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", "Bearer "+f.token.Secret)
		return localClient(handler).Transport.RoundTrip(r)
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cs, e := mcp.NewClient(&mcp.Implementation{Name: "http-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "http://mcp.test/mcp", HTTPClient: client}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	call(t, cs, "inventory_search", map[string]any{"query": "MOSFET"})
	if e = f.s.RevokeToken(ctx, f.admin, f.token.Token.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = cs.CallTool(ctx, &mcp.CallToolParams{Name: "inventory_search", Arguments: map[string]any{}}); e == nil {
		t.Fatal("revoked HTTP token accepted")
	}
}

// Optional real stdio subprocess smoke test, after building bin/inventrospect-mcp.
func TestStdioBinary(t *testing.T) {
	binary := os.Getenv("INVENTROSPECT_MCP_TEST_BINARY")
	if binary == "" {
		t.Skip("set INVENTROSPECT_MCP_TEST_BINARY to exercise the built stdio executable")
	}
	f := setup(t, inventory.TokenScopes...)
	server := httptest.NewServer(f.api)
	defer server.Close()
	cmd := exec.Command(binary, "-base-url", server.URL)
	cmd.Env = append(os.Environ(), "INVENTROSPECT_API_TOKEN="+f.token.Secret)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cs, e := mcp.NewClient(&mcp.Implementation{Name: "stdio-smoke", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	r := call(t, cs, "inventory_search", map[string]any{"query": "MOSFET"})
	if !strings.Contains(resultJSON(t, r), "MOSFET") {
		t.Fatal(resultJSON(t, r))
	}
	fmt.Fprintln(os.Stderr, "stdio executable negotiated and returned inventory data")
}

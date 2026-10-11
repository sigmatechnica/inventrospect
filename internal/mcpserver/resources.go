package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"inventrospect/internal/inventory"
)

func addResources(server *mcp.Server, b *Backend) {
	handler := func(ctx context.Context, r *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		u, e := url.Parse(r.Params.URI)
		if e != nil || u.Scheme != "inventory" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, validation("Invalid inventory resource URI")
		}
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) < 1 {
			return nil, validation("Missing resource ID")
		}
		id, e := strconv.ParseInt(parts[0], 10, 64)
		if e != nil || id <= 0 {
			return nil, validation("Resource ID must be positive")
		}
		var out any
		switch {
		case u.Host == "documents" && len(parts) == 1:
			content, e := documentContent(ctx, b, id)
			if e != nil {
				return nil, e
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{content}}, nil
		case u.Host == "items" && len(parts) == 1:
			out, e = getItem(ctx, b, id)
		case u.Host == "items" && len(parts) == 2 && parts[1] == "documents":
			var docs []inventory.Document
			e = b.JSON(ctx, "GET", fmt.Sprintf("/items/%d/documents", id), nil, &docs)
			if e == nil {
				out, e = slicePage(docs, PageInput{Limit: 100})
			}
		case u.Host == "locations" && len(parts) == 1:
			var nodes []inventory.Node
			e = b.JSON(ctx, "GET", "/locations", nil, &nodes)
			if e == nil {
				e = inventory.ErrNotFound
				for _, n := range nodes {
					if n.ID == id {
						out = n
						e = nil
						break
					}
				}
			}
		default:
			return nil, validation("Unknown inventory resource")
		}
		if e != nil {
			return nil, e
		}
		data, e := json.Marshal(out)
		if e != nil {
			return nil, e
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: r.Params.URI, MIMEType: "application/json", Text: string(data)}}}, nil
	}
	for _, r := range []struct{ uri, name, description, mime string }{
		{"inventory://items/{id}", "Item", "Item details; inventory:read. Stock/documents are included only with their corresponding read scopes.", "application/json"},
		{"inventory://items/{id}/documents", "Item documents", "First 100 document metadata records; documents:read. Use document_list for subsequent pages.", "application/json"},
		{"inventory://documents/{id}", "Stored document", "Actual stored binary bytes, up to 25 MiB, base64 on the wire; documents:read. No filesystem paths.", ""},
		{"inventory://locations/{id}", "Location", "Location ID, parent and full path; inventory:read.", "application/json"},
	} {
		server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: r.uri, Name: r.name, Description: r.description, MIMEType: r.mime}, handler)
	}
}

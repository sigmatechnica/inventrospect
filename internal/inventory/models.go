package inventory

import "fmt"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }
func invalid(format string, args ...any) error {
	return &Error{"validation", fmt.Sprintf(format, args...)}
}

var ErrForbidden = &Error{"forbidden", "You do not have permission to perform this operation"}
var ErrNotFound = &Error{"not_found", "Record not found"}
var ErrAuth = &Error{"unauthorized", "Invalid credentials or expired session"}

type User struct {
	Theme    string `json:"theme"`
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

// Actor is a trusted authentication result. Agent scopes and revocation are checked
// against the token record on each service authorization.
type Actor struct {
	TokenID int64
	UserID  int64
	Scopes  []string
}
type Item struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	CategoryID   *int64            `json:"category_id"`
	Manufacturer string            `json:"manufacturer"`
	MPN          string            `json:"mpn"`
	Condition    string            `json:"condition"`
	Notes        string            `json:"notes"`
	Tags         []string          `json:"tags"`
	Attributes   map[string]string `json:"attributes"`
	Archived     bool              `json:"archived"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
	CreatedBy    int64             `json:"created_by"`
	UpdatedBy    int64             `json:"updated_by"`
	Quantity     float64           `json:"quantity"`
}
type Node struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
	Path     string `json:"path"`
}
type Search struct {
	Query      string
	CategoryID int64
	LocationID int64
	Stock      string
	Archived   string
	Limit      int
	Offset     int
}
type Balance struct {
	LocationID int64   `json:"location_id"`
	Location   string  `json:"location"`
	Quantity   float64 `json:"quantity"`
}
type Movement struct {
	TokenID    *int64  `json:"token_id,omitempty"`
	ID         int64   `json:"id"`
	ItemID     int64   `json:"item_id"`
	LocationID int64   `json:"location_id"`
	Quantity   float64 `json:"quantity"`
	Reason     string  `json:"reason"`
	TransferID *string `json:"transfer_id"`
	ActorID    int64   `json:"actor_id"`
	CreatedAt  string  `json:"created_at"`
}
type Stock struct {
	Total    float64    `json:"total"`
	Balances []Balance  `json:"balances"`
	History  []Movement `json:"history"`
}
type Adjustment struct {
	LocationID int64   `json:"location_id,omitempty"`
	Quantity   float64 `json:"quantity"`
	Reason     string  `json:"reason,omitempty"`
}
type Transfer struct {
	FromLocationID int64   `json:"from_location_id"`
	ToLocationID   int64   `json:"to_location_id"`
	Quantity       float64 `json:"quantity"`
	Reason         string  `json:"reason,omitempty"`
}
type Document struct {
	ID           int64  `json:"id"`
	ItemID       int64  `json:"item_id"`
	Title        string `json:"title"`
	Filename     string `json:"filename"`
	Type         string `json:"type"`
	SourceURL    string `json:"source_url"`
	Manufacturer string `json:"manufacturer"`
	Revision     string `json:"revision"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	UploadedAt   string `json:"uploaded_at"`
	UploadedBy   int64  `json:"uploaded_by"`
	storageKey   string
}

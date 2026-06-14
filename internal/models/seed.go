package models

// JSON field names match procurement-web/lib/types.ts (SeedData).

type Vendor struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Logo       string  `json:"logo"`
	Category   string  `json:"category"`
	Contact    string  `json:"contact"`
	Email      string  `json:"email"`
	Phone      string  `json:"phone"`
	Country    string  `json:"country"`
	Terms      string  `json:"terms"`
	Rating     float64 `json:"rating"`
	Status     string  `json:"status"`
	TotalSpend float64 `json:"totalSpend"`
	OpenPOs    int     `json:"openPOs"`
}

type Item struct {
	ID               string  `json:"id"`
	SKU              string  `json:"sku"`
	Name             string  `json:"name"`
	Category         string  `json:"category"`
	UOM              string  `json:"uom"`
	Stock            float64 `json:"stock"`
	Reorder          float64 `json:"reorder"`
	LastPrice        float64 `json:"lastPrice"`
	Currency         string  `json:"currency"`
	PreferredVendor  string  `json:"preferredVendor"`
}

type Budget struct {
	ID         string  `json:"id"`
	Code       string  `json:"code"`
	Period     string  `json:"period"`
	Allocated  float64 `json:"allocated"`
	Committed  float64 `json:"committed"`
	Spent      float64 `json:"spent"`
	Remaining  float64 `json:"remaining"`
	Dept       string  `json:"dept"`
}

type Requisition struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Dept       string  `json:"dept"`
	Requester  string  `json:"requester"`
	Priority   string  `json:"priority"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"createdAt"`
	NeededBy   string  `json:"neededBy"`
	Total      float64 `json:"total"`
	Currency   string  `json:"currency"`
	BudgetID   string  `json:"budgetId"`
}

type Rfq struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Status          string   `json:"status"`
	DueDate         string   `json:"dueDate"`
	CreatedAt       string   `json:"createdAt"`
	WinnerVendor    *string  `json:"winnerVendor"`
	InvitedVendors  []string `json:"invitedVendors"`
}

// RfqQuote is a buyer-recorded vendor quote against an RFQ.
type RfqQuote struct {
	ID        string  `json:"id"`
	RfqID     string  `json:"rfqId"`
	VendorID  string  `json:"vendorId"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Notes     string  `json:"notes"`
	CreatedAt string  `json:"createdAt"`
	CreatedBy string  `json:"createdBy"`
}

type PoLine struct {
	ItemID string  `json:"itemId"`
	Qty    float64 `json:"qty"`
	Price  float64 `json:"price"`
}

type Po struct {
	ID           string   `json:"id"`
	VendorID     string   `json:"vendorId"`
	Title        string   `json:"title"`
	Total        float64  `json:"total"`
	Currency     string   `json:"currency"`
	Status       string   `json:"status"`
	CreatedAt    string   `json:"createdAt"`
	ExpectedDate string   `json:"expectedDate"`
	BudgetID     string   `json:"budgetId"`
	Items        []PoLine `json:"items"`
}

type Grn struct {
	ID            string  `json:"id"`
	PoID          *string `json:"poId"`
	VendorID      string  `json:"vendorId"`
	ReceivedDate  string  `json:"receivedDate"`
	ReceivedBy    string  `json:"receivedBy"`
	Status        string  `json:"status"`
}

type Invoice struct {
	ID           string  `json:"id"`
	InvoiceNo    *string `json:"invoiceNo,omitempty"`
	VendorID     string  `json:"vendorId"`
	PoID         *string `json:"poId"`
	Amount       float64 `json:"amount"`
	Total        *float64 `json:"total,omitempty"`
	Currency     string  `json:"currency"`
	Status       string  `json:"status"`
	MatchStatus  string  `json:"matchStatus"`
	InvoiceDate  string  `json:"invoiceDate"`
}

type Contract struct {
	ID        string  `json:"id"`
	VendorID  string  `json:"vendorId"`
	Title     string  `json:"title"`
	StartDate string  `json:"startDate"`
	EndDate   string  `json:"endDate"`
	Value     float64 `json:"value"`
	Currency  string  `json:"currency"`
	Status    string  `json:"status"`
}

type Payment struct {
	ID           string  `json:"id"`
	InvoiceID    string  `json:"invoiceId"`
	VendorID     string  `json:"vendorId"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Date         string  `json:"date"`
	Method       string  `json:"method"`
	Reference    string  `json:"reference"`
	Status       string  `json:"status"`
	InitiatedBy  string  `json:"initiatedBy"`
}

type AuditEntry struct {
	ID        int    `json:"id"`
	Timestamp string `json:"timestamp"`
	User      string `json:"user"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
}

type SeedData struct {
	Vendors       []Vendor       `json:"vendors"`
	Items         []Item         `json:"items"`
	Budgets       []Budget       `json:"budgets"`
	Requisitions  []Requisition  `json:"requisitions"`
	Rfqs          []Rfq          `json:"rfqs"`
	Pos           []Po           `json:"pos"`
	Grns          []Grn          `json:"grns"`
	Invoices      []Invoice      `json:"invoices"`
	Contracts     []Contract     `json:"contracts"`
	Payments      []Payment      `json:"payments"`
	Audit         []AuditEntry   `json:"audit"`
}

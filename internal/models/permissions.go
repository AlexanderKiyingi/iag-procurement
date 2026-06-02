package models

// PermissionDescriptor is posted to iag-authentication at startup.
type PermissionDescriptor struct {
	Name        string
	Description string
}

// PermissionDescriptors returns procurement domain permissions (excludes auth.* IAM).
func PermissionDescriptors() []PermissionDescriptor {
	return []PermissionDescriptor{
		{Name: "procurement.view_seed", Description: "Read master / seed API payloads"},
		{Name: "procurement.add_requisition", Description: "Create purchase requisitions"},
		{Name: "procurement.change_requisition", Description: "Update purchase requisitions"},
		{Name: "procurement.delete_requisition", Description: "Delete purchase requisitions"},
		{Name: "procurement.add_purchase_order", Description: "Create purchase orders with lines"},
		{Name: "procurement.change_purchase_order", Description: "Update purchase orders"},
		{Name: "procurement.delete_purchase_order", Description: "Delete purchase orders"},
		{Name: "procurement.add_vendor", Description: "Create vendor records"},
		{Name: "procurement.change_vendor", Description: "Update vendor records"},
		{Name: "procurement.delete_vendor", Description: "Delete vendor records"},
		{Name: "procurement.add_item", Description: "Create catalog items"},
		{Name: "procurement.change_item", Description: "Update catalog items"},
		{Name: "procurement.delete_item", Description: "Delete catalog items"},
		{Name: "procurement.add_budget", Description: "Create budget envelopes"},
		{Name: "procurement.change_budget", Description: "Update budget envelopes"},
		{Name: "procurement.delete_budget", Description: "Delete budget envelopes"},
		{Name: "procurement.add_rfq", Description: "Create requests for quotation"},
		{Name: "procurement.change_rfq", Description: "Update requests for quotation"},
		{Name: "procurement.delete_rfq", Description: "Delete requests for quotation"},
		{Name: "procurement.add_grn", Description: "Record goods receipts"},
		{Name: "procurement.change_grn", Description: "Update goods receipts"},
		{Name: "procurement.delete_grn", Description: "Delete goods receipts"},
		{Name: "procurement.add_invoice", Description: "Capture vendor invoices"},
		{Name: "procurement.change_invoice", Description: "Update vendor invoices"},
		{Name: "procurement.delete_invoice", Description: "Delete vendor invoices"},
		{Name: "procurement.add_contract", Description: "Create vendor contracts"},
		{Name: "procurement.change_contract", Description: "Update vendor contracts"},
		{Name: "procurement.delete_contract", Description: "Delete vendor contracts"},
		{Name: "audit.view_api_log", Description: "Read HTTP audit entries"},
		// Procurement-local inbox (not iag-notifications); registered under service name "procurement".
		{Name: "procurement.view_inbox", Description: "Read in-app notification inbox"},
		{Name: "procurement.emit_notification", Description: "Trigger signal / email demos"},
	}
}

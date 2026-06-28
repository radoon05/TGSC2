package woo

// BatchCreatePayload is the JSON body for POST /products/batch with create action.
type BatchCreatePayload struct {
	Create []*WooProduct `json:"create"`
}

// BatchUpdatePayload is the JSON body for POST /products/batch with update action.
type BatchUpdatePayload struct {
	Update []*WooProduct `json:"update"`
}

// BatchResponse represents the WooCommerce batch operation response.
type BatchResponse struct {
	Create []*WooProductResponse `json:"create,omitempty"`
	Update []*WooProductResponse `json:"update,omitempty"`
	Delete []interface{}         `json:"delete,omitempty"`
	Errors []*WooError           `json:"errors,omitempty"`
}

// WooProductResponse contains the created/updated product info from Woo.
type WooProductResponse struct {
	ID   int64  `json:"id"`
	SKU  string `json:"sku"`
}

// WooError represents a single error in batch response.
type WooError struct {
	ID      int64  `json:"id"`
	SKU     string `json:"sku"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BatchResult is the result of a batch operation, used by sync engine.
type BatchResult struct {
	SuccessSet map[string]bool   // source_id (SKU) -> success
	FailedIDs  map[string]string // source_id -> error message
}
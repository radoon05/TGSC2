package woo

// ============================================================
//  Structهای مربوط به Batch Operations
// ============================================================

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
	Create []WooProductResponse `json:"create,omitempty"`
	Update []WooProductResponse `json:"update,omitempty"`
	Delete []interface{}        `json:"delete,omitempty"`
	// دیگر فیلدهای top-level وجود ندارد؛ خطاها داخل آیتم‌ها هستند
}

// WooProductResponse contains the created/updated product info from Woo.
type WooProductResponse struct {
	ID    int64  `json:"id"`
	SKU   string `json:"sku"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"` // 🔥 اضافه شد
}

// BatchResult is the result of a batch operation, used by sync engine.
type BatchResult struct {
	SuccessSet  map[string]bool   // source_id → success
	FailedIDs   map[string]string // source_id → error message
	SKUToWooID  map[string]int64  // source_id → woo_id (برای ذخیره در دیتابیس) 🔥 جدید
}
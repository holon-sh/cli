package holon

import "encoding/json"

type OrdersInputRow = struct {
	Sku string "json:\"sku\""
}

type OrdersLookupInput = struct {
	ByRegion map[string]struct {
	Quantity *json.Number "json:\"quantity,omitempty\""
} "json:\"byRegion\""
	Id string "json:\"id\""
	Rows []OrdersInputRow "json:\"rows\""
}

type OrdersLookupOutput = struct {
	Total *json.Number "json:\"total,omitempty\""
}


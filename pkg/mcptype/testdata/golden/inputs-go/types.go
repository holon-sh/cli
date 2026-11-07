package holon

import "encoding/json"

type OrdersLookupInput = struct {
	ByRegion map[string]struct {
	Quantity *json.Number "json:\"quantity,omitempty\""
} "json:\"byRegion\""
	Id string "json:\"id\""
	Rows []struct {
	Sku string "json:\"sku\""
} "json:\"rows\""
}

type OrdersLookupInputRow = struct {
	Sku string "json:\"sku\""
}

type OrdersLookupOutput = struct {
	Total *json.Number "json:\"total,omitempty\""
}


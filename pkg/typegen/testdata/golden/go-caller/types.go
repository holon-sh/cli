package holon

import "encoding/json"

type OrdersLookupInput = struct {
	Id string "json:\"id\""
}

type OrdersLookupOutput = struct {
	Total json.Number "json:\"total\""
}


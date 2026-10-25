package holon

import "encoding/json"

type MysqlSelectInput = struct {
	Columns json.RawMessage "json:\"columns,omitempty\""
	Direction json.RawMessage "json:\"direction,omitempty\""
	Table string "json:\"table\""
}

type MysqlSelectInputDirection = json.RawMessage

type MysqlSelectInputLabeledColumn = struct {
	Column string "json:\"column\""
	Label string "json:\"label\""
}

type MysqlSelectOutput = struct {
	Columns []struct {
	Value json.Number "json:\"value\""
} "json:\"columns\""
}

type MysqlSelectOutputLabeledColumn = struct {
	Value json.Number "json:\"value\""
}

type MysqlSelectOutputRoot = struct {
	Columns []struct {
	Value json.Number "json:\"value\""
} "json:\"columns\""
}

type OrdersGetOrderInput = struct {
	Count *json.Number "json:\"count,omitempty\""
	OrderId string "json:\"orderId\""
}

type OrdersGetOrderOutput = map[string]json.RawMessage

type OrdersThenInput = map[string]json.RawMessage

type OrdersThenOutput = json.RawMessage

type WithHyphenGetOrderInput = struct {
	Count *json.Number "json:\"count,omitempty\""
	OrderId string "json:\"orderId\""
}

type WithHyphenGetOrderOutput = map[string]json.RawMessage

type WithHyphenThenInput = map[string]json.RawMessage

type WithHyphenThenOutput = json.RawMessage


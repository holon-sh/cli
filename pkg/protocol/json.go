package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func DecodeJSONStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func DecodeJSONFields(data []byte, target any, required ...string) error {
	if err := DecodeJSONStrict(data, target); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	for _, field := range required {
		if _, exists := fields[field]; !exists {
			return errors.New("missing required field: " + field)
		}
	}
	return nil
}

func DecodeJSONNonNull(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return DecodeJSONStrict(data, target)
		}
		if err != nil {
			return err
		}
		if token == nil {
			return errors.New("null fields are not supported; omit optional fields")
		}
	}
}

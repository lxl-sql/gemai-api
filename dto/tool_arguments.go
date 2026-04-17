package dto

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
)

// NormalizeConcatenatedSameJSONArgs collapses duplicated concatenated JSON values,
// e.g. {"k":"v"}{"k":"v"} -> {"k":"v"}.
// It only normalizes when every concatenated JSON value is semantically identical.
func NormalizeConcatenatedSameJSONArgs(arguments string) (string, bool) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return arguments, false
	}
	if json.Valid([]byte(trimmed)) {
		return arguments, false
	}

	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()

	var first any
	if err := decoder.Decode(&first); err != nil {
		return arguments, false
	}

	duplicated := false
	for {
		var next any
		err := decoder.Decode(&next)
		if err == io.EOF {
			break
		}
		if err != nil {
			return arguments, false
		}
		if !reflect.DeepEqual(first, next) {
			return arguments, false
		}
		duplicated = true
	}

	if !duplicated {
		return arguments, false
	}

	normalized, err := json.Marshal(first)
	if err != nil {
		return arguments, false
	}
	return string(normalized), true
}

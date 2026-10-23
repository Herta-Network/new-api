// Copyright 2026 The AstrLink Authors.
// SPDX-License-Identifier: Apache-2.0
//
// Ported from AstrLink convo (https://github.com/Calcium-Ion/AstrLink, convo/
// at commit 8461bf2). Changed in new-api: moved into the root module as
// internal/module/conversation/internal/convo, and JSON is decoded through
// the host's common JSON wrappers.

// Package jsonx holds tolerant decoders for json.RawMessage values. Every
// helper returns ok=false instead of an error so protocol adapters can walk
// half-known payloads without branching on error types.
package jsonx

import (
	"bytes"
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
)

// IsNull reports whether raw is absent or the JSON literal null.
func IsNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// String decodes a JSON string.
func String(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var value string
	if common.Unmarshal(trimmed, &value) != nil {
		return "", false
	}
	return value, true
}

// Bool decodes a JSON boolean.
func Bool(raw json.RawMessage) (bool, bool) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// Int decodes a JSON number that is an integer.
func Int(raw json.RawMessage) (int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' || trimmed[0] == '{' || trimmed[0] == '[' {
		return 0, false
	}
	var value int
	if common.Unmarshal(trimmed, &value) != nil {
		return 0, false
	}
	return value, true
}

// Array decodes a JSON array into its raw elements.
func Array(raw json.RawMessage) ([]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var items []json.RawMessage
	if common.Unmarshal(trimmed, &items) != nil {
		return nil, false
	}
	return items, true
}

// Object decodes a JSON object into its raw members.
func Object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if common.Unmarshal(trimmed, &fields) != nil || fields == nil {
		return nil, false
	}
	return fields, true
}

// StringOrID accepts either a JSON string or an object carrying an "id"
// string, the two shapes protocols use for conversation references.
func StringOrID(raw json.RawMessage) (string, bool) {
	if value, ok := String(raw); ok {
		return value, true
	}
	object, ok := Object(raw)
	if !ok {
		return "", false
	}
	return String(object["id"])
}

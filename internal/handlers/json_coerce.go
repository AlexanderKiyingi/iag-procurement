package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// coerceScalarStrings rewrites string values in a decoded JSON object to the
// scalar Go type of the matching struct field (keyed by json tag). Only
// numeric/bool target fields are touched; genuine string fields, empty
// strings, and unparseable values are left as-is so real bad input still errors.
func coerceScalarStrings(t reflect.Type, m map[string]any) {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		s, ok := m[name].(string)
		if !ok || s == "" {
			continue
		}
		ft := t.Field(i).Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Float32, reflect.Float64:
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				m[name] = v
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				m[name] = v
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if v, err := strconv.ParseUint(s, 10, 64); err == nil {
				m[name] = v
			}
		case reflect.Bool:
			if v, err := strconv.ParseBool(s); err == nil {
				m[name] = v
			}
		}
	}
	// Recurse into nested struct / slice-of-struct fields (e.g. PO/GRN line
	// items) so {"items":[{"qty":"5"}]} is coerced too.
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		ft := t.Field(i).Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			if nested, ok := m[name].(map[string]any); ok {
				coerceScalarStrings(ft, nested)
			}
		case reflect.Slice, reflect.Array:
			et := ft.Elem()
			for et.Kind() == reflect.Ptr {
				et = et.Elem()
			}
			if et.Kind() != reflect.Struct {
				continue
			}
			if arr, ok := m[name].([]any); ok {
				for _, el := range arr {
					if nested, ok := el.(map[string]any); ok {
						coerceScalarStrings(et, nested)
					}
				}
			}
		}
	}
}

// coerceJSONScalars returns raw JSON with string-encoded numeric/bool values
// rewritten to match dst's struct field types. Handles a single object or an
// array of objects. On any problem returns raw unchanged (never makes decoding worse).
func coerceJSONScalars(raw []byte, dst any) []byte {
	t := reflect.TypeOf(dst)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil {
		return raw
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		var rows []map[string]any
		if json.Unmarshal(raw, &rows) != nil {
			return raw
		}
		et := t.Elem()
		for _, m := range rows {
			coerceScalarStrings(et, m)
		}
		if out, err := json.Marshal(rows); err == nil {
			return out
		}
	case reflect.Struct:
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return raw
		}
		coerceScalarStrings(t, m)
		if out, err := json.Marshal(m); err == nil {
			return out
		}
	}
	return raw
}

// bindJSONCoerced reads the request body, rewrites string-encoded numeric/bool
// scalars to match dst's struct field types (so a browser form that submits
// {"qty":"5"} no longer 400s on a float64 field), then binds via gin's JSON
// binder so existing `binding:"required"` validation still runs. On any read
// problem it falls back to the standard binder behaviour.
func bindJSONCoerced(c *gin.Context, dst any) error {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	raw = coerceJSONScalars(raw, dst)
	// Restore the body so binding.JSON (and any downstream reader) sees the
	// coerced bytes; this also preserves gin's validator engine.
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return c.ShouldBindWith(dst, binding.JSON)
}

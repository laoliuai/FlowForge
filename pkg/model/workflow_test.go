package model

import (
	"encoding/json"
	"testing"
)

func TestJSONBValueAndScan(t *testing.T) {
	original := JSONB{"name": "flowforge", "count": 2}

	value, err := original.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}

	data, ok := value.([]byte)
	if !ok {
		t.Fatalf("expected []byte value, got %T", value)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal value error: %v", err)
	}

	if decoded["name"] != "flowforge" {
		t.Fatalf("expected name flowforge, got %v", decoded["name"])
	}

	var scanned JSONB
	if err := scanned.Scan(data); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if scanned["name"] != "flowforge" {
		t.Fatalf("expected scanned name flowforge, got %v", scanned["name"])
	}
}

func TestJSONBGormDataType(t *testing.T) {
	value := JSONB{"ok": true}
	if value.GormDataType() != "jsonb" {
		t.Fatalf("expected jsonb data type, got %q", value.GormDataType())
	}
}

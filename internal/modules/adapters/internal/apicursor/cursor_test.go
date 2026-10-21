package apicursor //nolint:testpackage // Cursor compatibility belongs to the transport owner.

import "testing"

func TestAdapterCursorsPreserveBytesAndScope(t *testing.T) {
	t.Parallel()
	cursor, err := EncodeAdapters("simulator")
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "eyJ2IjoxLCJyZXNvdXJjZSI6ImFkYXB0ZXJzIiwiaWQiOiJzaW11bGF0b3IifQ" {
		t.Fatalf("Adapter cursor bytes = %q", cursor)
	}
	id, err := DecodeAdapters(cursor)
	if err != nil || *id != "simulator" {
		t.Fatalf("Adapter cursor = %q, %v", cursor, err)
	}
	health, err := EncodeHealth("simulator", 42)
	if err != nil {
		t.Fatal(err)
	}
	if health != "eyJ2IjoxLCJyZXNvdXJjZSI6ImFkYXB0ZXJfaGVhbHRoIiwicGFyZW50X2lkIjoic2ltdWxhdG9yIiwicmVjZWl2ZV9vcmRlciI6NDJ9" {
		t.Fatalf("health cursor bytes = %q", health)
	}
	order, err := DecodeHealth(health, "simulator")
	if err != nil || *order != 42 {
		t.Fatalf("health cursor = %q, %v", health, err)
	}
	if _, decodeErr := DecodeHealth(health, "other"); decodeErr == nil {
		t.Fatal("health cursor accepted for another Adapter")
	}
	if _, decodeErr := DecodeAdapters(health); decodeErr == nil {
		t.Fatal("health cursor accepted for Adapter collection")
	}
}

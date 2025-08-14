package protocol

import (
	"bufio"
	"strings"
	"testing"
)

func TestResponseParsing(t *testing.T) {
	// Test parsing of simple HD response
	response1 := "HD Ob432aa59\r\n"
	reader1 := bufio.NewReader(strings.NewReader(response1))

	resp1, err := ReadResponse(reader1)
	if err != nil {
		t.Fatalf("readResponse() error = %v", err)
	}

	if resp1.Status != "HD" {
		t.Errorf("Expected status HD, got %s", resp1.Status)
	}

	if resp1.Opaque != "b432aa59" {
		t.Errorf("Expected opaque b432aa59, got %s", resp1.Opaque)
	}

	// Test parsing of two responses in sequence
	responses := "HD Ob432aa59\r\nHD O6fcfe440\r\n"
	reader2 := bufio.NewReader(strings.NewReader(responses))

	// First response
	resp2a, err := ReadResponse(reader2)
	if err != nil {
		t.Fatalf("readResponse() first error = %v", err)
	}

	if resp2a.Status != "HD" {
		t.Errorf("First response: Expected status HD, got %s", resp2a.Status)
	}

	if resp2a.Opaque != "b432aa59" {
		t.Errorf("First response: Expected opaque b432aa59, got %s", resp2a.Opaque)
	}

	// Second response
	resp2b, err := ReadResponse(reader2)
	if err != nil {
		t.Fatalf("readResponse() second error = %v", err)
	}

	if resp2b.Status != "HD" {
		t.Errorf("Second response: Expected status HD, got %s", resp2b.Status)
	}

	if resp2b.Opaque != "6fcfe440" {
		t.Errorf("Second response: Expected opaque 6fcfe440, got %s", resp2b.Opaque)
	}
}

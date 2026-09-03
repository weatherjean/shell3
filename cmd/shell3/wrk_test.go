//go:build unix

package main

import (
	"strings"
	"testing"
)

func TestReadWrkRequestDoesNotWaitForInteractiveInput(t *testing.T) {
	got, err := readWrkRequest(strings.NewReader("ignored"), true)
	if err != nil || got != "" {
		t.Fatalf("request = %q, err = %v", got, err)
	}
}

func TestReadWrkRequestReadsPipedInput(t *testing.T) {
	got, err := readWrkRequest(strings.NewReader("weather in Lenart\n"), false)
	if err != nil || got != "weather in Lenart" {
		t.Fatalf("request = %q, err = %v", got, err)
	}
}

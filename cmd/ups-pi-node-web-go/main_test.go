package main

import (
	"os"
	"strings"
	"testing"
)

func TestReadCredentialUsesBoundedSystemdCredentialFile(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("CREDENTIALS_DIRECTORY", directory)
	if err := os.WriteFile(directory+"/provider-token", []byte(strings.Repeat("h", 32)+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	value, err := readCredential("provider-token")
	if err != nil || value != strings.Repeat("h", 32) {
		t.Fatalf("credential length = %d, err=%v", len(value), err)
	}
}

func TestReadCredentialIsOptionalOutsideSystemd(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	if value, err := readCredential("provider-token"); err != nil || value != "" {
		t.Fatalf("credential = %q, err=%v", value, err)
	}
}

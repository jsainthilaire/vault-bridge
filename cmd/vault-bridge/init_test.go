package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIVaultInitSelectedProjectCanBeConnected(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(parent, "new vault")
	var out, diag bytes.Buffer
	if err := run(context.Background(), []string{"vault", "init", "--path", vault, "--id", "my-app", "--title", "My App"}, &out, &diag); err != nil {
		t.Fatalf("vault init: %v; %s", err, &diag)
	}
	if !strings.Contains(out.String(), "Vault initialized") {
		t.Fatalf("missing success output: %s", &out)
	}
	out.Reset()
	if err := run(context.Background(), []string{"config", "check", "--target", filepath.Join(vault, "03-Projects/my-app")}, &out, &diag); err != nil {
		t.Fatalf("generated target unusable: %v; %s", err, &diag)
	}
	if !strings.Contains(out.String(), "project my-app (0 shared grants)") {
		t.Fatalf("unexpected selected scope: %s", &out)
	}
}

func TestCLIVaultInitProjectFlagsRejectBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"only id", []string{"--id", "my-app"}},
		{"only title", []string{"--title", "My App"}},
		{"empty id", []string{"--id=", "--title", "My App"}},
		{"empty title", []string{"--id", "my-app", "--title="}},
		{"both empty", []string{"--id=", "--title="}},
		{"invalid id", []string{"--id", "../escape", "--title", "My App"}},
		{"invalid title", []string{"--id", "my-app", "--title", "My App\nInjected"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{"vault", "init", "--path", filepath.Join(parent, "missing", "vault")}, tc.args...)
			var out, diag bytes.Buffer
			if err := run(context.Background(), args, &out, &diag); err == nil {
				t.Fatal("invalid flags accepted")
			}
			if out.Len() != 0 {
				t.Errorf("invalid flags produced success output: %s", &out)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid flags caused filesystem writes: %v; %v", entries, err)
			}
		})
	}
}

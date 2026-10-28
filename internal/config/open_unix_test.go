//go:build unix

package config

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

func TestFIFOConfigurationRejectedWithoutBlocking(t *testing.T) {
	// Run the potentially blocking path in a child process so a regression
	// fails within the timeout instead of leaving a blocked test goroutine.
	if os.Getenv("VAULT_BRIDGE_TEST_FIFO_CHILD") == "1" {
		_, err := Load(os.Getenv("VAULT_BRIDGE_TEST_FIFO_PATH"), "alpha")
		if scope.ErrorCode(err) != "invalid_argument" || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("FIFO configuration: %v", err)
		}
		return
	}
	name := filepath.Join(t.TempDir(), "config.fifo")
	if err := syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFIFOConfigurationRejectedWithoutBlocking$")
	command.Env = append(os.Environ(), "VAULT_BRIDGE_TEST_FIFO_CHILD=1", "VAULT_BRIDGE_TEST_FIFO_PATH="+name)
	output, err := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("loading a FIFO configuration blocked")
	}
	if err != nil {
		t.Fatalf("FIFO subprocess failed: %v\n%s", err, output)
	}
}

func TestConfigOpenIsNonblockingForFIFO(t *testing.T) {
	// This exercises the open helper independently of the initial Stat check,
	// modeling the file becoming a FIFO between that check and open.
	if os.Getenv("VAULT_BRIDGE_TEST_FIFO_OPEN_CHILD") == "1" {
		file, err := openConfig(os.Getenv("VAULT_BRIDGE_TEST_FIFO_PATH"))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			t.Fatal("FIFO was incorrectly reported as regular")
		}
		return
	}
	name := filepath.Join(t.TempDir(), "swapped-config.fifo")
	if err := syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConfigOpenIsNonblockingForFIFO$")
	command.Env = append(os.Environ(), "VAULT_BRIDGE_TEST_FIFO_OPEN_CHILD=1", "VAULT_BRIDGE_TEST_FIFO_PATH="+name)
	output, err := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("configuration open blocked on a FIFO")
	}
	if err != nil {
		t.Fatalf("nonblocking open subprocess failed: %v\n%s", err, output)
	}
}

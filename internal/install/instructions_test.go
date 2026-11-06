package install

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstructionLifecycle(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, writes := range []bool{false, true} {
			t.Run(client+map[bool]string{false: "/read", true: "/write"}[writes], func(t *testing.T) {
				o := fixture(t, client)
				o.AllowWrites = writes
				// Alias differs from the physical folder; use the resolved identity.
				o.Target = filepath.Join(filepath.Dir(o.Config), "Test Vault/03-Projects/vault-bridge")
				o.Config, o.Project = "", "my-project"
				path := filepath.Join(o.Repo, instructionName(client))
				first, err := Project(o)
				if err != nil || !first.InstructionsChanged || first.InstructionsPath != path {
					t.Fatalf("install: %+v, %v", first, err)
				}
				data := read(t, path)
				if !bytes.Contains(data, []byte("`my-project`")) || bytes.Contains(data, []byte("expected_revision")) != writes {
					t.Fatalf("wrong generated instructions: %s", data)
				}
				assertInstructionWriteMode(t, data, writes)
				second, err := Project(o)
				if err != nil || second.Changed || second.InstructionsChanged || !bytes.Equal(data, read(t, path)) {
					t.Fatalf("repeat: %+v, %v", second, err)
				}
				// Refresh an old managed block while preserving authored text on both sides.
				prefix, suffix := "# Existing\r\nKeep this.\r\n", "\r\n## Other tool\r\nKeep this too.\r\n"
				put(t, path, prefix+instructionStart+"\nUpdate vault notes only when requested.\n"+instructionEnd+suffix)
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
				third, err := Project(o)
				if err != nil || third.Changed || !third.InstructionsChanged {
					t.Fatalf("refresh instructions: %+v, %v", third, err)
				}
				updated := string(read(t, path))
				if !strings.HasPrefix(updated, prefix) || !strings.HasSuffix(updated, suffix) || strings.Count(updated, instructionStart) != 1 {
					t.Fatal("refresh changed authored text or duplicated block")
				}
				assertInstructionWriteMode(t, []byte(updated), writes)
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o640 {
					t.Fatalf("permissions changed: %v", err)
				}
				// Existing connection must repair a missing instruction file.
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				fourth, err := Project(o)
				if err != nil || fourth.Changed || !fourth.InstructionsChanged {
					t.Fatalf("repair: %+v, %v", fourth, err)
				}
			})
		}
	}
}

func assertInstructionWriteMode(t *testing.T, data []byte, writes bool) {
	t.Helper()
	if bytes.Contains(data, []byte("only when requested")) {
		t.Fatal("stale request-only write guidance")
	}
	if bytes.Contains(data, []byte("automatically update")) != writes {
		t.Fatal("automatic maintenance guidance does not match write access")
	}
	if writes {
		for _, rule := range []string{"before your final response", "skip edits when there is no durable project change", "implementation and validation evidence", "expected_revision", "truncated is false", "On conflict, reread and merge", "vault paths actually changed", "write tools", "approval policy", "leave the vault unchanged"} {
			if !bytes.Contains(data, []byte(rule)) {
				t.Fatalf("missing write maintenance rule %q", rule)
			}
		}
	} else if !bytes.Contains(data, []byte("This connection is read-only")) {
		t.Fatal("missing read-only instruction")
	}
}

func TestInstructionRefreshPreservesCodexToolPolicy(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "matched"}[bulk], func(t *testing.T) {
			o := fixture(t, "codex")
			path, server, repo := target(o), "devvault", o.Repo
			if bulk {
				o, path = matchingFixture(t, "codex")
				server, repo = "devvault-alpha", filepath.Join(o.SourceDir, "alpha")
			}
			o.AllowWrites = true
			run := func() error {
				if bulk {
					_, err := Projects(o)
					return err
				}
				_, err := Project(o)
				return err
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			before := string(read(t, path)) + "\n[mcp_servers." + server + ".tools.document_update]\napproval_mode = 'approve'\n"
			put(t, path, before)
			instructions := filepath.Join(repo, "AGENTS.md")
			put(t, instructions, "# Authored content\n\n"+instructionStart+"\nUpdate vault notes only when requested.\n"+instructionEnd+"\n")
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if string(read(t, path)) != before {
				t.Fatal("changed client approval policy or settings")
			}
			assertInstructionWriteMode(t, read(t, instructions), true)
			if !bytes.HasPrefix(read(t, instructions), []byte("# Authored content\n\n")) {
				t.Fatal("lost authored content")
			}
			o.AllowWrites = false
			if err := run(); err == nil {
				t.Fatal("tool policy bypassed a launch-setting conflict")
			}
			if string(read(t, path)) != before {
				t.Fatal("conflict changed settings")
			}
		})
	}
}

func TestUnsafeInstructionsDoNotChangeSettings(t *testing.T) {
	for _, problem := range []string{"symlink", "directory", "large", "invalid UTF-8", "NUL", "unclosed", "reversed", "duplicate", "inline"} {
		t.Run(problem, func(t *testing.T) {
			o := fixture(t, "codex")
			path := filepath.Join(o.Repo, "AGENTS.md")
			outside := filepath.Join(filepath.Dir(o.Repo), "sentinel.md")
			put(t, outside, "untouched")
			original := "# keep\nmodel = 'custom'\n"
			put(t, target(o), original)
			switch problem {
			case "symlink":
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "large":
				put(t, path, strings.Repeat("x", maxConfigBytes+1))
			case "invalid UTF-8":
				put(t, path, "\xff")
			case "NUL":
				put(t, path, "\x00")
			case "unclosed":
				put(t, path, instructionStart)
			case "reversed":
				put(t, path, instructionEnd+"\n"+instructionStart)
			case "duplicate":
				put(t, path, instructionStart+instructionEnd+instructionStart+instructionEnd)
			case "inline":
				put(t, path, "prose "+instructionStart+"\n"+instructionEnd)
			}
			if _, err := Project(o); err == nil {
				t.Fatal("expected refusal")
			}
			if string(read(t, target(o))) != original || string(read(t, outside)) != "untouched" {
				t.Fatal("changed settings or symlink target")
			}
		})
	}
}

func TestInstructionConflictPreservesConcurrentEdit(t *testing.T) {
	o := fixture(t, "codex")
	path := filepath.Join(o.Repo, "AGENTS.md")
	put(t, path, "before")
	edit, err := prepareInstructions(o.Repo, o.Client, "devvault", "vault-bridge", filepath.Join(filepath.Dir(o.Config), "Test Vault"), false)
	if err != nil {
		t.Fatal(err)
	}
	put(t, path, "concurrent edit")
	if err := edit.apply(&Result{}); err == nil {
		t.Fatal("overwrote concurrent instructions")
	}
	if string(read(t, path)) != "concurrent edit" {
		t.Fatal("lost concurrent edit")
	}
}

func TestInstructionsNeverWrittenInsideVault(t *testing.T) {
	o := fixture(t, "codex")
	vault := filepath.Join(filepath.Dir(o.Config), "Test Vault")
	before := treeSnapshot(t, vault)
	o.Repo = filepath.Join(vault, "03-Projects/vault-bridge")
	if _, err := Project(o); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("accepted vault as source: %v", err)
	}
	after := treeSnapshot(t, vault)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("installer changed vault")
	}
	alias := filepath.Join(filepath.Dir(vault), "TEST VAULT")
	if actual, err := os.Stat(alias); err == nil {
		original, err := os.Stat(vault)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(original, actual) {
			o.Repo = filepath.Join(alias, "03-Projects/vault-bridge")
			if _, err := Project(o); err == nil || !strings.Contains(err.Error(), "outside") {
				t.Fatalf("accepted case alias of vault as source: %v", err)
			}
		}
	}

	bulk, destination := matchingFixture(t, "claude")
	bulk.SourceDir = bulk.ProjectsDir
	before = treeSnapshot(t, bulk.ProjectsDir)
	if _, err := Projects(bulk); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("accepted bulk vault as source: %v", err)
	}
	if !reflect.DeepEqual(before, treeSnapshot(t, bulk.ProjectsDir)) {
		t.Fatal("bulk installer changed vault")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("bulk settings changed: %v", err)
	}
}

func TestBulkInstructionPreflight(t *testing.T) {
	o, destination := matchingFixture(t, "codex")
	put(t, filepath.Join(o.SourceDir, "vault-bridge/AGENTS.md"), instructionStart)
	if _, err := Projects(o); err == nil {
		t.Fatal("expected malformed instruction failure")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("settings created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(o.SourceDir, "alpha/AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("earlier instructions changed: %v", err)
	}
}

package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestInstallPersistsWriteOptIn(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, global := range []bool{false, true} {
			for _, direct := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/global=%v/target=%v", client, global, direct), func(t *testing.T) {
					var o Options
					var destination string
					if global {
						o, destination, _ = globalFixture(t, client)
					} else {
						o = fixture(t, client)
						destination = target(o)
					}
					o.AllowWrites = true
					args := []any{"serve", "--config", o.Config, "--project", o.Project, "--transport", "stdio", "--allow-writes"}
					if direct {
						o.Target = filepath.Join(filepath.Dir(o.Config), "Test Vault", "03-Projects/vault-bridge")
						args = []any{"serve", "--target", o.Target, "--project", o.Project, "--transport", "stdio", "--allow-writes"}
						o.Config, o.Project = "", ""
					}
					result, err := Project(o)
					if err != nil || !result.Changed {
						t.Fatalf("write-enabled install: %+v %v", result, err)
					}
					data := read(t, destination)
					var doc map[string]any
					key := "mcp_servers"
					if client == "codex" {
						err = toml.Unmarshal(data, &doc)
					} else {
						key = "mcpServers"
						err = json.Unmarshal(data, &doc)
					}
					if err != nil {
						t.Fatal(err)
					}
					got := doc[key].(map[string]any)["devvault"].(map[string]any)["args"]
					if !reflect.DeepEqual(got, args) {
						t.Fatalf("launch args = %v; want %v", got, args)
					}
					result, err = Project(o)
					if err != nil || result.Changed || !bytes.Equal(data, read(t, destination)) {
						t.Fatalf("repeat write-enabled install: %+v %v", result, err)
					}
					o.AllowWrites = false
					if _, err := Project(o); err == nil || !bytes.Equal(data, read(t, destination)) {
						t.Fatal("changed existing permission mode without resolving conflicting entry")
					}
				})
			}
		}
	}
}

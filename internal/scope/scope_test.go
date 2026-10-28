package scope

import (
	"path/filepath"
	"testing"
)

func TestRelativePathPolicy(t *testing.T) {
	for _, name := range []string{"", "../index.md", "a/../b.md", "a/./b.md", "/index.md", "a//b.md", "a\\b.md", "C:/index.md", "a%2fb.md", "a\x00.md", "a\n.md", "a/"} {
		t.Run(name, func(t *testing.T) {
			if err := RelativePath(name); err == nil {
				t.Fatalf("accepted %q", name)
			}
		})
	}
	for _, name := range []string{"03-Projects/a/index.md", "05-Knowledge/Go/étude.md"} {
		if err := RelativePath(name); err != nil {
			t.Fatalf("rejected %q: %v", name, err)
		}
	}
}

func TestContainmentAndVisibility(t *testing.T) {
	root := filepath.Join(t.TempDir(), "alpha")
	if !Contains(root, filepath.Join(root, "index.md")) {
		t.Fatal("child denied")
	}
	if Contains(root, root+"-private/index.md") {
		t.Fatal("lookalike prefix accepted")
	}
	for _, name := range []string{".hidden.md", "a/.obsidian/secret.md", "secrets/key.md", "assets/note.md", "attachments/picture.md", "node_modules/private.md"} {
		if Visible(name) {
			t.Fatalf("visible: %s", name)
		}
	}
	if !Visible("features/scoped-file-access.md") {
		t.Fatal("ordinary note hidden")
	}
}

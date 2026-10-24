// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMCPConfigMerge(t *testing.T) {
	cursor, _ := mcpConfigFor(".cursor/mcp.json")
	vscode, _ := mcpConfigFor(".vscode/mcp.json")
	// Another server and a setting are kept, numbers as written.
	old := []byte(`{"mcpServers": {"gopls": {"command": "gopls", "args": ["mcp"]}}, "x": 12345678901234567890}`)
	out, err := cursor.merge(old)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		MCPServers map[string]struct {
			Command string
			Args    []string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if a := got.MCPServers["anetos"]; a.Command != "go" || !slices.Equal(a.Args, []string{"-C", "${workspaceFolder}", "tool", "anetos", "mcp"}) ||
		got.MCPServers["gopls"].Command != "gopls" || !strings.Contains(string(out), "12345678901234567890") {
		t.Errorf("merged:\n%s", out)
	}
	if !cursor.has(out) || cursor.has(old) {
		t.Error("has")
	}
	// Merging again changes nothing, bytes included.
	if again, err := cursor.merge(out); err != nil || string(again) != string(out) {
		t.Errorf("second merge: %v\n%s", err, again)
	}
	// An anetos entry is left as it is: the tool's, edited, or your own
	// (which isn't the tool's: has is false, remove leaves it).
	edited := []byte(`{"mcpServers": {"anetos": {"command": "go", "args": ["tool", "anetos", "mcp", "--dir", "/p"], "env": {"A": "1"}}}}`)
	if again, _ := cursor.merge(edited); string(again) != string(edited) || !cursor.has(edited) {
		t.Errorf("an edited entry: %s", again)
	}
	mine := []byte(`{"mcpServers": {"anetos": {"command": "/usr/local/bin/my-anetos"}}}`)
	if again, _ := cursor.merge(mine); string(again) != string(mine) || cursor.has(mine) {
		t.Errorf("your own anetos: %s", again)
	}
	if _, had, _, _ := cursor.remove(mine); had {
		t.Error("your own anetos was removed")
	}
	// A BOM is kept; VS Code's entries have a type, a cwd and the key servers.
	if out, err := cursor.merge(append([]byte("\xef\xbb\xbf"), old...)); err != nil || !strings.HasPrefix(string(out), "\xef\xbb\xbf{") {
		t.Errorf("BOM: %v %q", err, out)
	}
	if out := string(vscode.fresh()); !strings.Contains(out, `"servers"`) || !strings.Contains(out, `"type": "stdio"`) || !strings.Contains(out, `"cwd": "${workspaceFolder}"`) {
		t.Errorf("VS Code's file:\n%s", out)
	}
	// A file with comments isn't changed: the error says what to add.
	if _, err := vscode.merge([]byte("{\n  // mine\n  \"servers\": {}\n}")); err == nil || !strings.Contains(err.Error(), `"anetos":{`) {
		t.Errorf("JSONC: %v", err)
	}
	// Removing the entry keeps the rest; the last one empties the file.
	out2, had, empty, err := cursor.remove(out)
	if err != nil || !had || empty || strings.Contains(string(out2), "anetos") || !strings.Contains(string(out2), "gopls") {
		t.Errorf("remove: %v %v %v\n%s", had, empty, err, out2)
	}
	if _, had, empty, err := cursor.remove(cursor.fresh()); err != nil || !had || !empty {
		t.Errorf("remove the only entry: %v %v %v", had, empty, err)
	}
}

func TestMCPConfigTOML(t *testing.T) {
	codex, _ := mcpConfigFor(".codex/config.toml")
	old := "model = \"o4\"\n\n[mcp_servers.gopls]\ncommand = \"gopls\"\n"
	out, err := codex.merge([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if want := old + "\n" + tomlEntry; string(out) != want {
		t.Errorf("merged:\n%s\nwant:\n%s", out, want)
	}
	if again, _ := codex.merge(out); string(again) != string(out) || !codex.has(out) {
		t.Errorf("second merge:\n%s", again)
	}
	// A server named anetos, in any form TOML allows, is left as it is:
	// never a second definition.
	for _, defined := range []string{
		"[ mcp_servers.anetos ]\ncommand = \"go\"\n",
		"[mcp_servers.\"anetos\"]\ncommand = \"go\"\n",
		"[mcp_servers.anetos] # ours\ncommand = \"go\"\n",
		"[mcp_servers]\nanetos = { command = \"go\" }\n",
		"mcp_servers.anetos.command = \"go\"\n",
		"[mcp_servers.anetos.env]\nA = \"1\"\n",
	} {
		if out, _ := codex.merge([]byte(defined)); string(out) != defined {
			t.Errorf("merged into %q:\n%s", defined, out)
		}
	}
	// Removing takes the table and its subtables, keeps the next table's
	// comments, and leaves a server that isn't the tool's.
	file := "a = 1\n\n" + strings.Replace(tomlEntry, "tool_timeout_sec = 900\n", "tool_timeout_sec = 900\n\n[mcp_servers.anetos.env]\nX = \"1\"\n", 1) +
		"\n# the profile\n[profiles.x]\nmodel = \"y\"\n"
	removed, had := removeTOML([]byte(file))
	if !had || string(removed) != "a = 1\n\n# the profile\n[profiles.x]\nmodel = \"y\"\n" {
		t.Errorf("removed:\n%s", removed)
	}
	if _, had := removeTOML([]byte("[mcp_servers.anetos]\ncommand = \"mine\"\n")); had {
		t.Error("removed a server that isn't the tool's")
	}
	// CRLF files get CRLF.
	if out, _ := codex.merge([]byte("a = 1\r\n")); !strings.Contains(string(out), "command = \"go\"\r\n") {
		t.Errorf("CRLF: %q", out)
	}
}

// agent:install writes each agent's MCP configuration; --agent prunes
// the others' entries, keeping the files' other servers.
func TestInstallMCPConfigs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/shop\n\ngo 1.26\n")
	writeFile(t, root, ".cursor/mcp.json", `{"mcpServers": {"other": {"command": "x"}}}`)
	p, err := ReadAgentProject(root)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := InstallAgentFiles(root, p, Agents, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range mcpConfigs {
		if !slices.Contains(changed, c.rel) || !mcpIn(root, c.rel) {
			t.Errorf("%s not written: %v", c.rel, changed)
		}
	}
	if got := InstalledAgents(root); !slices.Equal(got, []string{"claude", "codex", "copilot", "cursor", "gemini"}) {
		t.Errorf("InstalledAgents = %v", got)
	}
	if stale := mustStale(t, root); len(stale) != 0 {
		t.Errorf("stale: %v", stale)
	}
	writeFile(t, root, ".gemini/settings.json", `{"theme": "x"}`)
	if stale := mustStale(t, root); len(stale) != 0 { // gemini isn't installed any more: its server is gone
		t.Errorf("stale without Gemini's server: %v", stale)
	}
	if _, err := InstallAgentFiles(root, p, []string{"claude"}, true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".vscode/mcp.json", ".codex/config.toml"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s kept: %v", rel, err)
		}
	}
	if got := readFile(t, root, ".cursor/mcp.json"); strings.Contains(got, "anetos") || !strings.Contains(got, "other") {
		t.Errorf(".cursor/mcp.json:\n%s", got)
	}
	if !mcpIn(root, ".mcp.json") {
		t.Error(".mcp.json lost the server")
	}
	// A file that can't be merged doesn't stop the others.
	writeFile(t, root, ".vscode/mcp.json", "{ // mine\n}")
	changed, err = InstallAgentFiles(root, p, []string{"copilot", "codex"}, false)
	if err == nil || !strings.Contains(err.Error(), ".vscode/mcp.json") || !slices.Contains(changed, ".codex/config.toml") {
		t.Errorf("an unmergeable file: %v %v", changed, err)
	}
}

// agent:update after installing some agents keeps exactly those.
func TestInstalledAgentsByMCP(t *testing.T) {
	for _, agents := range [][]string{{"gemini"}, {"claude", "gemini"}, {"cursor", "codex"}, {"copilot"}, {"claude", "copilot"}} {
		root := t.TempDir()
		writeFile(t, root, "go.mod", "module example.com/shop\n\ngo 1.26\n")
		p, err := ReadAgentProject(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := InstallAgentFiles(root, p, agents, true); err != nil {
			t.Fatal(err)
		}
		want := slices.Sorted(slices.Values(agents))
		if got := InstalledAgents(root); !slices.Equal(got, want) {
			t.Errorf("%v: InstalledAgents = %v", agents, got)
		}
		if stale := mustStale(t, root); len(stale) != 0 {
			t.Errorf("%v: stale %v", agents, stale)
		}
	}
}

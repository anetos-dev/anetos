// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// mcpServerName is the server's name in the agents' MCP configuration.
const mcpServerName = "anetos"

// mcpConfig is a file in which an agent reads its MCP servers, in the
// project.
type mcpConfig struct {
	rel    string   // the file, relative to the project
	agents []string // the agents that read it
	key    string   // the JSON object of the servers; "" for TOML
	// entry is the server's entry (JSON's), extra fields included.
	entry func() map[string]any
}

// mcpConfigs are the files agent:install writes the server's entry in
// (design D327). An agent starts a project's server in the project:
// Cursor and VS Code are told so (${workspaceFolder}), as their docs
// don't promise it.
var mcpConfigs = []mcpConfig{
	// Claude Code's project servers; Copilot CLI reads it too.
	{rel: ".mcp.json", agents: []string{"claude", "copilot"}, key: "mcpServers", entry: func() map[string]any {
		return map[string]any{"command": "go", "args": toAny("tool", "anetos", "mcp")}
	}},
	{rel: ".vscode/mcp.json", agents: []string{"copilot"}, key: "servers", entry: func() map[string]any {
		return map[string]any{"type": "stdio", "command": "go", "args": toAny("tool", "anetos", "mcp"), "cwd": "${workspaceFolder}"}
	}},
	{rel: ".cursor/mcp.json", agents: []string{"cursor"}, key: "mcpServers", entry: func() map[string]any {
		return map[string]any{"command": "go", "args": toAny("-C", "${workspaceFolder}", "tool", "anetos", "mcp")}
	}},
	// timeout: a check runs the tests (milliseconds).
	{rel: ".gemini/settings.json", agents: []string{"gemini"}, key: "mcpServers", entry: func() map[string]any {
		return map[string]any{"command": "go", "args": toAny("tool", "anetos", "mcp"), "timeout": json.Number("900000")}
	}},
	{rel: ".codex/config.toml", agents: []string{"codex"}},
}

// mcpConfigFor returns the configuration file rel, or false.
func mcpConfigFor(rel string) (mcpConfig, bool) {
	i := slices.IndexFunc(mcpConfigs, func(c mcpConfig) bool { return c.rel == rel })
	if i < 0 {
		return mcpConfig{}, false
	}
	return mcpConfigs[i], true
}

// wanted reports whether one of the agents reads the file.
func (c mcpConfig) wanted(agents []string) bool {
	return slices.ContainsFunc(c.agents, func(a string) bool { return slices.Contains(agents, a) })
}

func toAny(s ...string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// tomlEntry is the server's table in Codex's config.toml. Codex's
// timeouts are short for go tool's first start and for check, which
// runs the tests.
const tomlEntry = `[mcp_servers.anetos]
command = "go"
args = ["tool", "anetos", "mcp"]
startup_timeout_sec = 120
tool_timeout_sec = 900
`

// fresh is the file with only the server's entry.
func (c mcpConfig) fresh() []byte {
	if c.key == "" {
		return []byte(tomlEntry)
	}
	return encodeJSON(map[string]any{c.key: map[string]any{mcpServerName: c.entry()}})
}

// ours reports whether a server's entry runs anetos mcp: its args end
// with tool anetos mcp (and options), whatever comes first (go -C dir).
func ours(entry any) bool {
	m, ok := entry.(map[string]any)
	if !ok || m["command"] != "go" {
		return false
	}
	args, _ := m["args"].([]any)
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "tool" && args[i+1] == "anetos" && args[i+2] == "mcp" {
			return true
		}
	}
	return false
}

// utf8BOM starts some files written on Windows.
var utf8BOM = []byte("\xef\xbb\xbf")

// merge adds the server's entry to the file old, keeping the other
// servers and settings. An entry named anetos is left as it is, the
// tool's (which you may have edited: an env, --dir) or your own: old
// comes back unchanged. A JSON file is rewritten with its keys sorted
// (JSON doesn't keep their order); one that doesn't parse (comments,
// say) is an error that says what to add.
func (c mcpConfig) merge(old []byte) ([]byte, error) {
	if c.key == "" {
		if tomlDefines(old) {
			return old, nil
		}
		return appendTOML(old), nil
	}
	doc, servers, err := c.parse(old)
	if err != nil {
		return nil, err
	}
	if _, taken := servers[mcpServerName]; taken {
		return old, nil
	}
	servers[mcpServerName] = c.entry()
	doc[c.key] = servers
	out := encodeJSON(doc)
	if bytes.HasPrefix(old, utf8BOM) {
		out = append(slices.Clip(utf8BOM), out...)
	}
	return out, nil
}

// remove takes the tool's entry out of old (not a server of yours named
// anetos). ok is false when it has none; empty, when nothing else is
// left (the file can go).
func (c mcpConfig) remove(old []byte) (out []byte, ok, empty bool, err error) {
	if c.key == "" {
		out, ok = removeTOML(old)
		return out, ok, ok && strings.TrimSpace(string(out)) == "", nil
	}
	doc, servers, err := c.parse(old)
	if err != nil {
		return nil, false, false, err
	}
	if !ours(servers[mcpServerName]) {
		return old, false, false, nil
	}
	delete(servers, mcpServerName)
	if len(servers) == 0 {
		delete(doc, c.key)
	} else {
		doc[c.key] = servers
	}
	out = encodeJSON(doc)
	if bytes.HasPrefix(old, utf8BOM) {
		out = append(slices.Clip(utf8BOM), out...)
	}
	return out, true, len(doc) == 0, nil
}

// has reports whether old has the tool's entry (an entry that runs
// anetos mcp).
func (c mcpConfig) has(old []byte) bool {
	if c.key == "" {
		return tomlOurs(old)
	}
	_, servers, err := c.parse(old)
	return err == nil && ours(servers[mcpServerName])
}

func (c mcpConfig) parse(old []byte) (doc, servers map[string]any, err error) {
	doc = map[string]any{}
	body := bytes.TrimPrefix(old, utf8BOM)
	if len(bytes.TrimSpace(body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber() // 12345678901234567890 stays as written
		if err := dec.Decode(&doc); err != nil {
			return nil, nil, fmt.Errorf("%s isn't plain JSON (%w): add the anetos server to %q yourself: %s", c.rel, err, c.key, entryJSON(c))
		}
	}
	servers = map[string]any{}
	if v, ok := doc[c.key]; ok {
		m, isMap := v.(map[string]any)
		if !isMap {
			return nil, nil, fmt.Errorf("%s: %q isn't an object", c.rel, c.key)
		}
		servers = maps.Clone(m)
	}
	return doc, servers, nil
}

func entryJSON(c mcpConfig) string {
	b, _ := json.Marshal(map[string]any{mcpServerName: c.entry()})
	return string(b)
}

func encodeJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return b.Bytes()
}

var (
	// tomlHeader is the server's table header, in the forms TOML allows
	// ([ mcp_servers . "anetos" ] # a comment); tomlSub, a table under it
	// ([mcp_servers.anetos.env]).
	tomlHeader = regexp.MustCompile(`^\s*\[\s*mcp_servers\s*\.\s*("anetos"|'anetos'|anetos)\s*\]\s*(#.*)?\s*$`)
	tomlSub    = regexp.MustCompile(`^\s*\[\s*mcp_servers\s*\.\s*("anetos"|'anetos'|anetos)\s*\.`)
	// tomlOther defines the server another way: a dotted key
	// (mcp_servers.anetos.command = …) or, in [mcp_servers], anetos = {…}.
	tomlOther   = regexp.MustCompile(`^\s*(mcp_servers\s*\.\s*)?("anetos"|'anetos'|anetos)\s*(\.|=)`)
	tomlServers = regexp.MustCompile(`^\s*\[\s*mcp_servers\s*\]\s*(#.*)?\s*$`)
	anyHeader   = regexp.MustCompile(`^\s*\[`)
)

// tomlLines splits a file into lines, each with its line ending.
func tomlLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.SplitAfter(string(b), "\n")
}

// tomlDefines reports whether a config.toml defines a server named
// anetos, in any form.
func tomlDefines(b []byte) bool {
	table := "" // "" at the top level, "servers" in [mcp_servers], "other"
	for _, line := range tomlLines(b) {
		switch {
		case tomlHeader.MatchString(line), tomlSub.MatchString(line):
			return true
		case tomlServers.MatchString(line):
			table = "servers"
		case anyHeader.MatchString(line):
			table = "other"
		case table == "" && strings.HasPrefix(strings.TrimSpace(line), "mcp_servers") && tomlOther.MatchString(line),
			table == "servers" && tomlOther.MatchString(line):
			return true
		}
	}
	return false
}

// tomlOurs reports whether the anetos server a config.toml defines runs
// anetos mcp.
func tomlOurs(b []byte) bool {
	if !tomlDefines(b) {
		return false
	}
	for _, line := range tomlLines(b) {
		if l := strings.ReplaceAll(line, " ", ""); strings.Contains(l, `"tool","anetos","mcp"`) {
			return true
		}
	}
	return false
}

// appendTOML adds the server's table after the rest of the file.
func appendTOML(old []byte) []byte {
	s := string(old)
	nl := "\n"
	if strings.Contains(s, "\r\n") {
		nl = "\r\n"
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += nl
	}
	if strings.TrimSpace(s) != "" {
		s += nl
	}
	return []byte(s + strings.ReplaceAll(tomlEntry, "\n", nl))
}

// removeTOML takes the server's tables ([mcp_servers.anetos] and those
// under it) out of a config.toml, when it's the tool's: up to the next
// table, but the comments right above that table, which are its. A
// server defined another way is left alone.
func removeTOML(old []byte) ([]byte, bool) {
	if !tomlOurs(old) {
		return old, false
	}
	lines := tomlLines(old)
	var out []string
	removed := false
	for i := 0; i < len(lines); {
		if !tomlHeader.MatchString(lines[i]) && !tomlSub.MatchString(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		removed = true
		end := i + 1
		for end < len(lines) && !anyHeader.MatchString(lines[end]) {
			end++
		}
		keep := end // the next table's comments
		for keep > i+1 && strings.HasPrefix(strings.TrimSpace(lines[keep-1]), "#") {
			keep--
		}
		out = append(out, lines[keep:end]...)
		i = end
	}
	if !removed {
		return old, false // defined as a dotted key or an inline table
	}
	return []byte(strings.TrimLeft(strings.Join(out, ""), "\r\n")), true
}

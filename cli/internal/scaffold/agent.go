// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

// Agents are the coding agents the agent files are written for. Every one
// reads AGENTS.md; the others' files are:
//
//   - claude (Claude Code): CLAUDE.md, importing AGENTS.md, and the
//     skills in .claude/skills/
//   - gemini (Gemini CLI): GEMINI.md, importing AGENTS.md, and the skills
//     in .agents/skills/
//   - codex, copilot (GitHub Copilot), cursor: the skills in
//     .agents/skills/
var Agents = []string{"claude", "codex", "copilot", "cursor", "gemini"}

// The markers around the part of AGENTS.md that the tool writes; the
// developer's own text goes outside them.
const (
	agentsBegin = "<!-- anetos:begin: go tool anetos agent:update rewrites the text between these markers; add your own notes outside them -->"
	agentsEnd   = "<!-- anetos:end -->"
	agentImport = "@AGENTS.md"
)

// Skill folders, and the prefix of the skills the tool writes: a skill
// named otherwise is the developer's, and left alone.
const (
	sharedSkills = ".agents/skills"
	claudeSkills = ".claude/skills"
	skillPrefix  = "anetos-"
)

// skill is one of the agent files' skills.
type skill struct {
	Name, Summary string
	stack         string // "web" or "api" for one stack only; "" for both
}

var skills = []skill{
	{"anetos-crud", "a resource: model, table, pages or endpoints", ""},
	{"anetos-auth", "accounts, login, protecting routes, roles", ""},
	{"anetos-migration", "schema changes", ""},
	{"anetos-job", "queued jobs, events, scheduled tasks", ""},
	{"anetos-page", "pages, forms and components", "web"},
	{"anetos-api", "JSON endpoints", "api"},
	{"anetos-test", "tests", ""},
	{"anetos-deploy", "building and deploying", ""},
	{"anetos-security", "checking security and fixing findings", ""},
	{"anetos-upgrade", "upgrading Anetos", ""},
	{"anetos-plugin", "plugins", ""},
}

// AgentProject is what the agent files say about a project.
type AgentProject struct {
	Name   string // the app's name ("blog"): its deploy unit's, its binary's
	Module string // its module path, which names the binary
	Stack  string // "web" or "api"
	DB     string // "sqlite", "postgres" or "mysql"
	Kit    string // the web stack's CSS framework (Kits)
	// HasUI is set when the web project has views/ui's components
	// (projects made before v0.5 don't).
	HasUI bool
}

// agentData is what the agent templates see.
type agentData struct {
	Title, Name, Bin, Image, DB, DBLabel, KitSentence string
	Web, API, HasUI                                   bool
	Skills                                            []skill
	SkillDirs                                         []string
}

var kitSentences = map[string]string{
	"anetos":    "The pages use the starter theme, Anetos's own stylesheet, through the components in `views/ui`.",
	"none":      "The pages are plain HTML without a CSS framework, written by the components in `views/ui`.",
	"pico":      "The pages use Pico CSS, through the components in `views/ui`.",
	"bootstrap": "The pages use Bootstrap, through the components in `views/ui`.",
	"bulma":     "The pages use Bulma, through the components in `views/ui`.",
	"tailwind":  "The pages use Tailwind CSS, through the components in `views/ui` (`views/ui/tailwind.css` is compiled into `public/static/app.css`).",
}

// noUISentence is a web project's without views/ui.
const noUISentence = "The pages are templ views with their own markup and `public/static/app.css`."

var dbLabels = map[string]string{"sqlite": "SQLite", "postgres": "PostgreSQL", "mysql": "MySQL"}

func (p AgentProject) data(agents []string) agentData {
	d := agentData{
		Title: title(p.Name), Name: p.Name, Bin: BinaryName(cmp.Or(p.Module, p.Name)), Image: strings.ToLower(p.Name),
		DB: p.DB, DBLabel: dbLabels[p.DB], KitSentence: kitSentences[p.Kit],
		Web: p.Stack != "api", API: p.Stack == "api", HasUI: p.HasUI && p.Stack != "api",
		SkillDirs: skillDirs(agents),
	}
	if d.Web && !d.HasUI {
		d.KitSentence = noUISentence
	}
	stack := "web"
	if d.API {
		stack = "api"
	}
	for _, s := range skills {
		if s.stack == "" || s.stack == stack {
			d.Skills = append(d.Skills, s)
		}
	}
	return d
}

// AgentFile is one of the agent files, relative to the project.
type AgentFile struct {
	Rel     string
	Content []byte
}

// skillDirs are the skill folders the agents read.
func skillDirs(agents []string) []string {
	var dirs []string
	for _, a := range agents {
		dir := sharedSkills
		if a == "claude" {
			dir = claudeSkills
		}
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	slices.Sort(dirs)
	return dirs
}

// RenderAgentFiles renders the agent files of a project for the agents
// (a subset of Agents): AGENTS.md, with its text between the markers,
// CLAUDE.md and GEMINI.md, which import it, and the skills in the agents'
// folders.
func RenderAgentFiles(p AgentProject, agents []string) ([]AgentFile, error) {
	for _, a := range agents {
		if !slices.Contains(Agents, a) {
			return nil, fmt.Errorf("unknown agent %q: the agents are %s", a, strings.Join(Agents, ", "))
		}
	}
	d := p.data(agents)
	block, err := render("templates/agent/AGENTS.md.tmpl", "", d)
	if err != nil {
		return nil, err
	}
	files := []AgentFile{{"AGENTS.md", []byte(agentsBegin + "\n" + string(block) + agentsEnd + "\n")}}
	if slices.Contains(agents, "claude") {
		files = append(files, AgentFile{"CLAUDE.md", []byte(agentImport + "\n")})
	}
	if slices.Contains(agents, "gemini") {
		files = append(files, AgentFile{"GEMINI.md", []byte(agentImport + "\n")})
	}
	for _, s := range d.Skills {
		content, err := render("templates/agent/skills/"+s.Name+"/SKILL.md.tmpl", "", d)
		if err != nil {
			return nil, err
		}
		for _, dir := range d.SkillDirs {
			files = append(files, AgentFile{dir + "/" + s.Name + "/SKILL.md", content})
		}
	}
	for _, c := range mcpConfigs {
		if c.wanted(agents) {
			files = append(files, AgentFile{c.rel, c.fresh()})
		}
	}
	return files, nil
}

// lf turns a file's CRLF line endings into LF, for comparing: a Windows
// checkout (git's core.autocrlf) has CRLF.
func lf(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// crlf gives b the line endings of like: CRLF when like has them.
func crlf(b, like []byte) []byte {
	if bytes.Contains(like, []byte("\r\n")) {
		return bytes.ReplaceAll(lf(b), []byte("\n"), []byte("\r\n"))
	}
	return b
}

// InstallAgentFiles writes the agent files for the agents into the
// project in root, keeping what the developer wrote: in AGENTS.md, the
// text outside the markers (a file without them gets the tool's text
// first, then its own); in CLAUDE.md and GEMINI.md, everything, with the
// import of AGENTS.md added first when it's missing. The skills named
// anetos-* are rewritten; others are left alone. A file that differs
// only in its line endings is left as it is. With prune, the anetos-*
// skills in a folder none of the agents reads are removed, so the files
// are those of exactly these agents. It returns the files it created,
// changed or removed, relative to root.
func InstallAgentFiles(root string, p AgentProject, agents []string, prune bool) ([]string, error) {
	files, err := RenderAgentFiles(p, agents)
	if err != nil {
		return nil, err
	}
	var changed []string
	var mcpErrs []error // an MCP file that can't be merged doesn't stop the others
	for _, f := range files {
		file := filepath.Join(root, filepath.FromSlash(f.Rel))
		old, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return changed, err
		}
		content := f.Content
		if err == nil {
			switch f.Rel {
			case "AGENTS.md":
				if content, err = mergeAgents(old, f.Content); err != nil {
					return changed, err
				}
			case "CLAUDE.md", "GEMINI.md":
				content = addImport(old)
			default:
				if c, ok := mcpConfigFor(f.Rel); ok {
					if content, err = c.merge(old); err != nil {
						mcpErrs = append(mcpErrs, err)
						continue
					}
				}
			}
			if bytes.Equal(lf(old), lf(content)) {
				continue
			}
			content = crlf(content, old)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return changed, err
		}
		if err := os.WriteFile(file, content, 0o644); err != nil {
			return changed, err
		}
		changed = append(changed, f.Rel)
	}
	if prune {
		keep := skillDirs(agents)
		for _, dir := range []string{claudeSkills, sharedSkills} {
			if slices.Contains(keep, dir) {
				continue
			}
			for _, name := range anetosSkills(root, dir) {
				rel := dir + "/" + name
				if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
					return changed, err
				}
				changed = append(changed, rel+"/ (removed)")
			}
		}
		for _, c := range mcpConfigs {
			if c.wanted(agents) {
				continue
			}
			file := filepath.Join(root, filepath.FromSlash(c.rel))
			old, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			out, had, empty, err := c.remove(old)
			if err != nil {
				mcpErrs = append(mcpErrs, err)
				continue
			}
			switch {
			case !had:
				continue
			case empty:
				err = os.Remove(file)
			default:
				err = os.WriteFile(file, crlf(out, old), 0o644)
			}
			if err != nil {
				return changed, err
			}
			changed = append(changed, c.rel+" (the anetos server removed)")
		}
	}
	return changed, errors.Join(mcpErrs...)
}

// anetosSkills are the anetos-* skill folders (with a SKILL.md) in dir.
func anetosSkills(root, dir string) []string {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), skillPrefix) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), e.Name(), "SKILL.md")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names
}

// beginPrefix finds the begin marker, whatever its comment says.
const beginPrefix = "<!-- anetos:begin"

// mergeAgents puts the tool's text (block, markers included) into an
// existing AGENTS.md: between its markers, or first when it has none. A
// begin marker without an end marker after it is an error: the file was
// edited, and guessing could lose text.
func mergeAgents(old, block []byte) ([]byte, error) {
	s := string(old)
	i := strings.Index(s, beginPrefix)
	if i < 0 {
		out := append(crlf(block, old), crlf([]byte("\n"), old)...)
		return append(out, old...), nil
	}
	j := strings.Index(s[i:], agentsEnd)
	if j < 0 {
		return nil, fmt.Errorf("AGENTS.md has %q without %q after it: put the end marker back, or remove the begin marker, then run the command again", beginPrefix, agentsEnd)
	}
	j += i
	rest := s[j+len(agentsEnd):]
	rest = strings.TrimPrefix(strings.TrimPrefix(rest, "\r"), "\n")
	return []byte(s[:i] + string(crlf(block, old)) + rest), nil
}

// addImport adds the import of AGENTS.md first in a CLAUDE.md or
// GEMINI.md that lacks it.
func addImport(old []byte) []byte {
	if imports(old) {
		return old
	}
	return append(crlf([]byte(agentImport+"\n\n"), old), old...)
}

// imports reports whether a CLAUDE.md or GEMINI.md imports AGENTS.md.
func imports(b []byte) bool {
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.TrimSpace(line) == agentImport {
			return true
		}
	}
	return false
}

// InstalledAgents returns the agents whose files are in the project in
// root, as far as their skills tell: claude for .claude/skills/anetos-*;
// for .agents/skills/anetos-*, the folder codex, copilot, cursor and
// gemini share, those of codex, copilot and cursor whose MCP
// configuration has the anetos server (codex when none has: a project
// from before the server); gemini when GEMINI.md imports AGENTS.md and
// .gemini/settings.json has the server (or the project has no MCP
// configuration at all).
// Nil without an AGENTS.md written by the tool. (Removing a skill
// folder removes its agents: their files are no longer checked or
// updated.)
func InstalledAgents(root string) []string {
	b, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || !bytes.Contains(b, []byte(beginPrefix)) {
		return nil
	}
	agents := []string{}
	if len(anetosSkills(root, claudeSkills)) > 0 {
		agents = append(agents, "claude")
	}
	if len(anetosSkills(root, sharedSkills)) > 0 {
		// The agents that share the folder, by their MCP configuration.
		var shared []string
		for _, a := range []string{"codex", "copilot", "cursor"} {
			for _, c := range mcpConfigs {
				if c.rel != ".mcp.json" && slices.Contains(c.agents, a) && mcpIn(root, c.rel) { // .mcp.json is claude's too
					shared = append(shared, a)
				}
			}
		}
		shared = slices.Compact(shared)
		hasGemini := mcpIn(root, ".gemini/settings.json")
		// codex stands for them all, and GEMINI.md's import alone for
		// gemini (GEMINI.md is the developer's too), only in a project
		// without any MCP configuration: from before the server.
		old := len(shared) == 0 && !hasGemini && !mcpIn(root, ".mcp.json")
		if old {
			shared = []string{"codex"}
		}
		agents = append(agents, shared...)
		if b, err := os.ReadFile(filepath.Join(root, "GEMINI.md")); err == nil && imports(b) && (hasGemini || old) {
			agents = append(agents, "gemini")
		}
	}
	return agents
}

// mcpIn reports whether the configuration file rel has the server.
func mcpIn(root, rel string) bool {
	c, _ := mcpConfigFor(rel)
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && c.has(b)
}

// StaleAgentFiles returns the agent files of the project in root that
// differ from what this tool writes (an older Anetos's, or edited inside
// the markers), line endings aside: AGENTS.md's text between the markers
// and the anetos-* skills. Nil when the project has no agent files.
func StaleAgentFiles(root string) ([]string, error) {
	agents := InstalledAgents(root)
	if agents == nil {
		return nil, nil
	}
	p, err := ReadAgentProject(root)
	if err != nil {
		return nil, err
	}
	files, err := RenderAgentFiles(p, agents)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, f := range files {
		if f.Rel == "CLAUDE.md" || f.Rel == "GEMINI.md" {
			continue // the developer's, once it imports AGENTS.md
		}
		old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Rel)))
		if err != nil {
			stale = append(stale, f.Rel)
			continue
		}
		want := f.Content
		if f.Rel == "AGENTS.md" {
			if want, err = mergeAgents(old, f.Content); err != nil {
				return nil, err
			}
		} else if c, ok := mcpConfigFor(f.Rel); ok {
			if want, err = c.merge(old); err != nil {
				return nil, err
			}
		}
		if !bytes.Equal(lf(old), lf(want)) {
			stale = append(stale, f.Rel)
		}
	}
	return stale, nil
}

// ReadAgentProject reads what the agent files say about the project in
// root: its name (its deploy unit's, deploy/<name>.service, which anetos
// new names after the project, else its binary's; not the directory's,
// which a clone or a worktree may change), its stack (api with
// routes/api.go and no views/), its database (the driver go.mod
// requires), its CSS framework (GuessKit) and whether it has views/ui.
func ReadAgentProject(root string) (AgentProject, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return AgentProject{}, err
	}
	p := AgentProject{Stack: "web", DB: "sqlite"}
	_, apiErr := os.Stat(filepath.Join(root, "routes", "api.go"))
	_, viewsErr := os.Stat(filepath.Join(root, "views"))
	if apiErr == nil && viewsErr != nil {
		p.Stack = "api"
	}
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return p, err
	}
	f, err := modfile.ParseLax("go.mod", b, nil)
	if err != nil {
		return p, err
	}
	if f.Module != nil {
		p.Module = f.Module.Mod.Path
	}
	for _, r := range f.Require {
		if db, ok := strings.CutPrefix(r.Mod.Path, "anetos.dev/anetos/drivers/"); ok && dbLabels[db] != "" {
			p.DB = db
			break
		}
	}
	var units []string
	entries, _ := os.ReadDir(filepath.Join(root, "deploy"))
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".service"); ok && !e.IsDir() && n != "" {
			units = append(units, n)
		}
	}
	if len(units) == 1 {
		p.Name = units[0]
	}
	if p.Name == "" && p.Module != "" {
		p.Name = BinaryName(p.Module)
	}
	if p.Name == "" {
		p.Name = filepath.Base(abs)
	}
	if p.Stack == "web" {
		p.Kit = GuessKit(root)
		p.HasUI = HasUI(root)
	}
	return p, nil
}

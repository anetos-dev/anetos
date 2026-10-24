// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"anetos.dev/anetos/cli/internal/scaffold"
)

const agentInstallUsage = `Usage: anetos agent:install [--agent=claude,codex,copilot,cursor,gemini]

Writes the files coding agents (Claude Code, Codex, GitHub Copilot,
Cursor, Gemini CLI) read in this project, so they build with Anetos as
its docs say:

  AGENTS.md              the project, the commands, where things go, the
                         conventions, and "run go tool anetos check
                         before calling a change done"
  CLAUDE.md, GEMINI.md   one line importing AGENTS.md (Claude Code, Gemini CLI)
  .agents/skills/anetos-*/SKILL.md   task guides (Codex, Copilot, Cursor, Gemini CLI)
  .claude/skills/anetos-*/SKILL.md   the same, for Claude Code

anetos new writes them already (--no-agent skips them). Your own text in
AGENTS.md goes outside its markers, and your own skills under other
names: both are kept. Commit the files, so everyone's agent reads the
same. The files are those of exactly the agents listed: Anetos's skills
in a folder none of them reads are removed. (A coding agent works on
your code; make:agent writes an AI agent that runs in your app: two
different things.)
`

const agentUpdateUsage = `Usage: anetos agent:update

Rewrites the agent files that agent:install (or anetos new) wrote, for
this version of Anetos: AGENTS.md's text between its markers, and the
anetos-* skills, for the agents whose files are there. Run it after
upgrading Anetos; doctor says when the files are out of date. Your own
text and skills are kept.
`

// agentInstall runs anetos agent:install.
func agentInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos agent:install", flag.ContinueOnError)
	list := fs.String("agent", strings.Join(scaffold.Agents, ","), "the coding agents, comma-separated: "+strings.Join(scaffold.Agents, ", "))
	pos, code := parse(fs, args, stderr, agentInstallUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fs.Usage()
		return 2
	}
	agents, err := parseAgents(*list)
	if err != nil {
		fmt.Fprintln(stderr, "anetos agent:install:", err)
		return 2
	}
	return writeAgentFiles("agent:install", agents, true, stdout, stderr)
}

// agentUpdate runs anetos agent:update.
func agentUpdate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos agent:update", flag.ContinueOnError)
	pos, code := parse(fs, args, stderr, agentUpdateUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fs.Usage()
		return 2
	}
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(stderr, "anetos agent:update:", err)
		return 1
	}
	agents := scaffold.InstalledAgents(root)
	if agents == nil {
		fmt.Fprintln(stderr, "anetos agent:update: the project has no agent files: write them with go tool anetos agent:install")
		return 1
	}
	return writeAgentFiles("agent:update", agents, false, stdout, stderr)
}

// writeAgentFiles writes the agent files; with prune (agent:install),
// for exactly these agents, removing the anetos-* skills of the others.
func writeAgentFiles(name string, agents []string, prune bool, stdout, stderr io.Writer) int {
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintf(stderr, "anetos %s: %v\n", name, err)
		return 1
	}
	p, err := scaffold.ReadAgentProject(root)
	if err != nil {
		fmt.Fprintf(stderr, "anetos %s: %v\n", name, err)
		return 1
	}
	changed, err := scaffold.InstallAgentFiles(root, p, agents, prune)
	for _, f := range changed {
		if dir, ok := strings.CutSuffix(f, "/ (removed)"); ok {
			fmt.Fprintf(stdout, "removed %s/\n", dir)
			continue
		}
		fmt.Fprintf(stdout, "wrote %s\n", f)
	}
	if err != nil {
		fmt.Fprintf(stderr, "anetos %s: %v\n", name, err)
		return 1
	}
	if len(changed) == 0 {
		fmt.Fprintln(stdout, "The agent files are up to date.")
	}
	return 0
}

// parseAgents parses --agent's list.
func parseAgents(list string) ([]string, error) {
	var agents []string
	for a := range strings.SplitSeq(list, ",") {
		a = strings.ToLower(strings.TrimSpace(a))
		switch {
		case a == "":
		case !slices.Contains(scaffold.Agents, a):
			return nil, fmt.Errorf("unknown agent %q: the agents are %s", a, strings.Join(scaffold.Agents, ", "))
		case !slices.Contains(agents, a):
			agents = append(agents, a)
		}
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("no agent given: the agents are %s", strings.Join(scaffold.Agents, ", "))
	}
	return agents, nil
}

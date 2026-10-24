// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"text/tabwriter"

	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/internal/secret"
)

// about is what the about command reports of an app, for a developer
// or a coding agent: what it is, what it runs, and its settings.
type about struct {
	Name     string `json:"name"`
	Env      string `json:"env"`
	Debug    bool   `json:"debug"`
	URL      string `json:"url,omitempty"`
	Timezone string `json:"timezone"`
	// Anetos and Go are the versions the app was built with; Module is
	// its module path.
	Anetos string `json:"anetos"`
	Go     string `json:"go"`
	Module string `json:"module,omitempty"`
	// Services are the types of the services provided ([Provide]):
	// "*db.DB", "*queue.Queue"…
	Services []string `json:"services"`
	// ProcessTypes are the process types its components run in.
	ProcessTypes []string `json:"process_types"`
	// Components are its components, with their process types.
	Components []aboutComponent `json:"components"`
	// Settings are the settings the app read while booting, with their
	// values: "(set)" for a secret (by its name), "<value> (default)" for
	// a default, "<value> (from <former name>)", "(not set)" when unset
	// without a default; a connection string's password and a URL's user
	// are masked.
	Settings map[string]string `json:"settings"`
}

// aboutComponent is a component in about.
type aboutComponent struct {
	Name         string   `json:"name"`
	ProcessTypes []string `json:"process_types,omitempty"`
}

// secretName matches the names of settings whose values are secrets.
var secretName = regexp.MustCompile(`(?i)(KEY|SECRET|PASSWORD|PASSWD|PASS|TOKEN|CREDENTIAL|PRIVATE|DSN|WEBHOOK)`)

// settingValue is a setting's value as about shows it: a secret's is
// "(set)" (by its name), and secrets in a value (a connection string's
// password, a URL's user) are masked (package secret).
func settingValue(name, value string) string {
	switch {
	case value == "":
		return ""
	case secretName.MatchString(name):
		return "(set)"
	}
	return secret.MaskValue(value)
}

// about describes the app, booted: its services provided, its
// settings read.
func (a *App) about() about {
	ab := about{Name: a.cfg.Name, Env: string(a.cfg.Env), Debug: a.cfg.Debug, URL: a.cfg.URL,
		Timezone: a.Location().String(), Anetos: Version(),
		Go:       runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		Services: []string{}, ProcessTypes: a.sup.ProcessTypes(), Components: []aboutComponent{},
		Settings: map[string]string{}}
	if bi, ok := debug.ReadBuildInfo(); ok {
		ab.Module = bi.Main.Path
	}
	a.svcMu.RLock()
	for t := range a.services {
		ab.Services = append(ab.Services, t.String())
	}
	a.svcMu.RUnlock()
	slices.Sort(ab.Services)
	for _, c := range a.sup.Status() {
		ab.Components = append(ab.Components, aboutComponent{Name: c.Name, ProcessTypes: c.ProcessTypes})
	}
	slices.SortFunc(ab.Components, func(x, y aboutComponent) int { return cmp.Compare(x.Name, y.Name) })
	if ab.ProcessTypes == nil {
		ab.ProcessTypes = []string{}
	}
	if t, ok := a.src.(*trackedSource); ok {
		t.mu.Lock()
		names := make([]string, 0, len(t.read))
		for name := range t.read {
			if _, old := t.former[name]; !old {
				names = append(names, name)
			}
		}
		defaults := maps.Clone(t.dflt)
		from := map[string]string{} // a setting → the former name its value came from
		for old, current := range t.renamed {
			from[current] = old
		}
		t.mu.Unlock()
		for _, name := range names {
			ab.Settings[name] = describeSetting(t.Source, name, from[name], defaults)
		}
	}
	return ab
}

// describeSetting is the value about shows for the setting name: its
// value, "<value> (from <former>)" when it came from its former name,
// "<default> (default)", or "(not set)".
func describeSetting(src config.Source, name, former string, defaults map[string]string) string {
	if v, ok := src.Lookup(name); ok && v != "" {
		return settingValue(name, v)
	}
	if former != "" {
		if v, ok := src.Lookup(former); ok && v != "" {
			return settingValue(name, v) + " (from " + former + ")"
		}
	}
	if def, ok := defaults[name]; ok {
		if def == "" || secretName.MatchString(name) {
			return "(default)"
		}
		return settingValue(name, def) + " (default)"
	}
	return "(not set)"
}

func (a *App) aboutCommand() cmd.Command {
	return cmd.Command{
		Name:        "about",
		Usage:       "[--json]",
		Description: "Describe the app: versions, services, process types and the settings it reads (secrets hidden)",
		Run: func(_ context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("about", flag.ContinueOnError)
			asJSON := fs.Bool("json", false, "print it as JSON")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 0 {
				return cmd.Usagef("unexpected argument %q", fs.Arg(0))
			}
			ab := a.about()
			if *asJSON {
				enc := json.NewEncoder(args.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(ab)
			}
			return writeAbout(args.Stdout, ab)
		},
	}
}

func writeAbout(w io.Writer, ab about) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\n", ab.Name)
	fmt.Fprintf(tw, "  Environment\tAPP_ENV=%s, APP_DEBUG=%t\n", ab.Env, ab.Debug)
	if ab.URL != "" {
		fmt.Fprintf(tw, "  URL\t%s\n", ab.URL)
	}
	fmt.Fprintf(tw, "  Timezone\t%s\n", ab.Timezone)
	fmt.Fprintf(tw, "  Module\t%s\n", cmp.Or(ab.Module, "(unknown)"))
	fmt.Fprintf(tw, "  Anetos\t%s\n", ab.Anetos)
	fmt.Fprintf(tw, "  Go\t%s\n", ab.Go)
	fmt.Fprintf(tw, "  Process types\t%s\n", cmp.Or(strings.Join(ab.ProcessTypes, ", "), "(none)"))
	fmt.Fprintln(tw, "\nServices")
	for _, s := range ab.Services {
		fmt.Fprintf(tw, "  %s\n", s)
	}
	fmt.Fprintln(tw, "\nComponents")
	for _, c := range ab.Components {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name, strings.Join(c.ProcessTypes, ", "))
	}
	fmt.Fprintln(tw, "\nSettings (read while booting; secrets hidden)")
	names := make([]string, 0, len(ab.Settings))
	for name := range ab.Settings {
		names = append(names, name)
	}
	slices.Sort(names)
	unset := 0
	for _, name := range names {
		if ab.Settings[name] == "(not set)" {
			unset++
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s\n", name, ab.Settings[name])
	}
	if unset > 0 {
		fmt.Fprintf(tw, "  (%d more read, not set and without a default: --json lists them)\n", unset)
	}
	return tw.Flush()
}

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"anetos.dev/anetos/cli/internal/devserver"
	"anetos.dev/anetos/cli/internal/scaffold"
	"anetos.dev/anetos/config"
)

const devUsage = `Usage: anetos dev [--addr=:8080] [--host=name]... [-- app arguments]

Runs the app with live reload. On every change to Go files, templ files,
.env or public/ (or views/ui/tailwind.css), it runs templ generate,
anetos generate and, in a project that uses Tailwind CSS, anetos
css:build, rebuilds, restarts the app and reloads the open
pages. Build errors show in the browser.
Browse the address given by --addr (default: HTTP_ADDR from .env, or
:8080, on 127.0.0.1 only); the app itself listens on a free local port.
Browsers reach it as localhost, a name under .localhost, an IP address,
APP_URL's host or a --host: other names get 403, so another site can't
reach it by pointing a name of its own at 127.0.0.1.
`

func dev(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos dev", flag.ContinueOnError)
	addr := fs.String("addr", "", "address to serve on (default: HTTP_ADDR, or :8080)")
	var hosts []string
	fs.Func("host", "another host name browsers may use (repeatable), besides localhost, IP addresses and APP_URL's", func(v string) error {
		hosts = append(hosts, v)
		return nil
	})
	var appArgs []string
	for i, a := range args {
		if a == "--" {
			args, appArgs = args[:i], args[i+1:]
			break
		}
	}
	pos, code := parse(fs, args, stderr, devUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fs.Usage()
		return 2
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "anetos dev:", err)
		return 1
	}
	root, err := scaffold.FindRoot(wd)
	if err != nil {
		fmt.Fprintln(stderr, "anetos dev:", err)
		return 1
	}
	src, _ := config.Load(config.LoadOptions{Dir: root})
	if src != nil {
		if v, ok := src.Lookup("APP_URL"); ok && v != "" {
			if u, err := url.Parse(v); err == nil && u.Hostname() != "" {
				hosts = append(hosts, u.Hostname())
			}
		}
	}
	if *addr == "" {
		*addr = ":8080"
		if src != nil {
			if v, ok := src.Lookup("HTTP_ADDR"); ok && v != "" {
				*addr = v
			}
		}
		// Only this machine, unless --addr says otherwise: the dev server
		// shows build errors and logs.
		if host, port, err := net.SplitHostPort(*addr); err == nil && host == "" {
			*addr = net.JoinHostPort("127.0.0.1", port)
		}
	}
	if host, _, err := net.SplitHostPort(*addr); err == nil {
		if ip, err := netip.ParseAddr(host); host != "localhost" && (err != nil || !ip.IsLoopback()) {
			fmt.Fprintf(stderr, "anetos dev: serving on %s, reachable from other machines: it shows build errors, logs and debug pages\n", *addr)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := devserver.Run(ctx, devserver.Options{Dir: root, Addr: *addr, Args: appArgs, Out: stdout, Hosts: hosts}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"anetos.dev/anetos/cli/internal/devserver"
	"anetos.dev/anetos/cli/internal/scaffold"
	"anetos.dev/anetos/config"
)

const devUsage = `Usage: anetos dev [--addr=:8080] [-- app arguments]

Runs the app with live reload. On every change to Go files, templ files,
.env or public/, it runs templ generate and anetos gen, rebuilds, restarts
the app and reloads the open pages. Build errors show in the browser.
Browse the address given by --addr (default: HTTP_ADDR from .env, or
:8080, on 127.0.0.1 only); the app itself listens on a free local port.
`

func dev(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos dev", flag.ContinueOnError)
	addr := fs.String("addr", "", "address to serve on (default: HTTP_ADDR, or :8080)")
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
	if *addr == "" {
		*addr = ":8080"
		if src, err := config.Load(config.LoadOptions{Dir: root}); err == nil {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := devserver.Run(ctx, devserver.Options{Dir: root, Addr: *addr, Args: appArgs, Out: stdout}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

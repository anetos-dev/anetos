// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"fmt"
	"time"

	"anetos.dev/anetos/config"
)

type Heartbeat struct {
	Interval time.Duration `env:"HEARTBEAT_INTERVAL" default:"30s"`
	URL      string        `env:"HEARTBEAT_URL,required"`
}

func ExampleGet() {
	src := config.Map{"HEARTBEAT_URL": "https://status.example.com/ping"}
	cfg, err := config.Get[Heartbeat](src)
	fmt.Println(cfg.Interval, cfg.URL, err)

	_, err = config.Get[Heartbeat](config.Map{"HEARTBEAT_INTERVAL": "soon"})
	fmt.Println(err)
	// Output:
	// 30s https://status.example.com/ping <nil>
	// config: HEARTBEAT_INTERVAL (Heartbeat.Interval): invalid duration "soon" (use values like 30s, 5m, 1h)
	// config: HEARTBEAT_URL (Heartbeat.URL): required but not set
}

module anetos.dev/anetos/drivers/redis

go 1.26.0

require (
	anetos.dev/anetos v0.5.0-rc.1
	github.com/redis/go-redis/v9 v9.23.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace anetos.dev/anetos => ../..

// Unusable: they require versions of other modules that don't exist.
retract [v0.1.0, v0.4.0]

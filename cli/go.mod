module anetos.dev/anetos/cli

go 1.26.0

require (
	anetos.dev/anetos v0.5.0-rc.1
	github.com/modelcontextprotocol/go-sdk v1.8.0
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/mod v0.41.0
	golang.org/x/term v0.46.0
	golang.org/x/tools v0.51.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

// Unusable: they require versions of other modules that don't exist.
retract [v0.1.0, v0.4.0]

module anetos.dev/anetos/drivers/openai

go 1.26.0

require (
	anetos.dev/anetos v0.5.0-rc.1
	github.com/openai/openai-go/v3 v3.74.0
)

require (
	github.com/coder/websocket v1.8.15 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace anetos.dev/anetos => ../..

// Unusable: they require versions of other modules that don't exist.
retract [v0.1.0, v0.4.0]

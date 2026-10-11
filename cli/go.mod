module anetos.dev/anetos/cli

go 1.26.0

require (
	anetos.dev/anetos v0.5.0-rc.1
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/mod v0.41.0
	golang.org/x/term v0.46.0
	golang.org/x/tools v0.51.0
)

require (
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

// Unusable: they require versions of other modules that don't exist.
retract [v0.1.0, v0.4.0]

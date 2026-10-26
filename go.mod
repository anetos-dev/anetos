module anetos.dev/anetos

go 1.26.0

require (
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/crypto v0.58.0
	golang.org/x/oauth2 v0.37.0
	golang.org/x/text v0.43.0
)

require golang.org/x/sys v0.49.0 // indirect

// Pre-releases from before Anetos was announced: use v0.5.0 or later.
retract [v0.1.0, v0.4.0]

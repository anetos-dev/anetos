module anetos.dev/anetos/examples/assistant

go 1.26.0

require (
	anetos.dev/anetos v0.0.0-00010101000000-000000000000
	anetos.dev/anetos/drivers/anthropic v0.0.0-00010101000000-000000000000
	anetos.dev/anetos/drivers/mysql v0.0.0-00010101000000-000000000000
	anetos.dev/anetos/drivers/openai v0.0.0-00010101000000-000000000000
	anetos.dev/anetos/drivers/postgres v0.0.0-00010101000000-000000000000
	anetos.dev/anetos/drivers/sqlite v0.0.0-00010101000000-000000000000
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/anthropics/anthropic-sdk-go v1.78.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-sql-driver/mysql v1.10.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.11.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/openai/openai-go/v3 v3.70.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.60.1 // indirect
)

replace (
	anetos.dev/anetos => ../..
	anetos.dev/anetos/drivers/anthropic => ../../drivers/anthropic
	anetos.dev/anetos/drivers/mysql => ../../drivers/mysql
	anetos.dev/anetos/drivers/openai => ../../drivers/openai
	anetos.dev/anetos/drivers/postgres => ../../drivers/postgres
	anetos.dev/anetos/drivers/sqlite => ../../drivers/sqlite
)

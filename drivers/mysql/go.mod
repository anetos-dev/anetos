module anetos.dev/anetos/drivers/mysql

go 1.26

require (
	github.com/go-sql-driver/mysql v1.10.1
	anetos.dev/anetos v0.0.0-00010101000000-000000000000
)

require filippo.io/edwards25519 v1.2.0 // indirect

replace anetos.dev/anetos => ../..

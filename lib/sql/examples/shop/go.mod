module github.com/nomi-language/nomi/lib/sql/examples/shop

go 1.27.0

require (
	github.com/nomi-language/nomi/lib/sql/sqlite v0.0.0-20261026231947-3434642c92e9
	github.com/nomi-language/nomi/lib/sql/sqlkit v0.0.0-20261026231947-3434642c92e9
)

require (
	github.com/nomi-language/nomi/lib/sql/migrate v0.0.0-20261102000000-000000000000 // indirect
	github.com/nomi-language/nomi/lib/sql/postgres v0.0.0-20261102000000-000000000000 // indirect
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.6 // indirect
	modernc.org/memory v1.11.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/sync v0.15.0 // indirect
	modernc.org/libc v1.66.3 // indirect
	modernc.org/sqlite v1.38.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.34.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

replace (
	github.com/nomi-language/nomi/lib/sql/migrate => ../../migrate
	github.com/nomi-language/nomi/lib/sql/postgres => ../../postgres
	github.com/nomi-language/nomi/lib/sql/sqlite => ../../sqlite
	github.com/nomi-language/nomi/lib/sql/sqlkit => ../../sqlkit
)

module github.com/nomi-language/nomi/librunners

go 1.27.0

require (
	github.com/nomi-language/nomi v0.0.0-00010101000000-000000000000
	github.com/nomi-language/nomi/lib/image v0.0.0-00010101000000-000000000000
	github.com/nomi-language/nomi/lib/markdown v0.0.0-00010101000000-000000000000
	github.com/nomi-language/nomi/lib/sql/postgres v0.0.0-20261102000000-000000000000
	github.com/nomi-language/nomi/lib/sql/sqlite v0.0.0-20261026231947-3434642c92e9
	github.com/nomi-language/nomi/lib/sql/sqlkit v0.0.0-00010101000000-000000000000
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/iancoleman/strcase v0.3.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.6 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/yuin/goldmark v1.8.7 // indirect
	golang.org/x/crypto v0.51.0 // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/image v0.31.0 // indirect
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	modernc.org/libc v1.66.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.38.2 // indirect
)

replace (
	github.com/nomi-language/nomi => ../
	github.com/nomi-language/nomi/lib/image => ../lib/image
	github.com/nomi-language/nomi/lib/markdown => ../lib/markdown
	github.com/nomi-language/nomi/lib/sql/migrate => ../lib/sql/migrate
	github.com/nomi-language/nomi/lib/sql/postgres => ../lib/sql/postgres
	github.com/nomi-language/nomi/lib/sql/sqlite => ../lib/sql/sqlite
	github.com/nomi-language/nomi/lib/sql/sqlkit => ../lib/sql/sqlkit
)

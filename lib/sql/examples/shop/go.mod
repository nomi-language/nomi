module github.com/nomi-language/nomi/lib/sql/examples/shop

go 1.27.0

require (
	github.com/nomi-language/nomi/lib/sql/sqlite v0.0.0-00010101000000-000000000000
	github.com/nomi-language/nomi/lib/sql/sqlkit v0.0.0-00010101000000-000000000000
)

replace (
	github.com/nomi-language/nomi/lib/sql/sqlite => ../../sqlite
	github.com/nomi-language/nomi/lib/sql/sqlkit => ../../sqlkit
)

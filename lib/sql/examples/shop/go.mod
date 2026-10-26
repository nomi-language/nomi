module github.com/nomi-language/nomi/lib/sql/examples/shop

go 1.27.0

require (
	github.com/nomi-language/nomi/lib/sql/sqlite v0.0.0-20261026231947-3434642c92e9
	github.com/nomi-language/nomi/lib/sql/sqlkit v0.0.0-20261026231947-3434642c92e9
)

replace (
	github.com/nomi-language/nomi/lib/sql/sqlite => ../../sqlite
	github.com/nomi-language/nomi/lib/sql/sqlkit => ../../sqlkit
)

module github.com/nomi-language/nomi/lib/jobs/examples/notify

go 1.27.0

require (
	github.com/nomi-language/nomi/lib/jobs/postgres v0.0.0-20261102115722-e15c01bbc477
	github.com/nomi-language/nomi/lib/sql/postgres v0.0.0-20261102115722-e15c01bbc477
)

replace (
	github.com/nomi-language/nomi/lib/jobs/postgres => ../../postgres
	github.com/nomi-language/nomi/lib/sql/postgres => ../../../sql/postgres
)

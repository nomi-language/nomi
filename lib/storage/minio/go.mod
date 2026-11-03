module github.com/nomi-language/nomi/lib/storage/minio

go 1.27.0

require github.com/nomi-language/nomi/lib/storage/s3 v0.0.0-20261102115722-e15c01bbc477

replace github.com/nomi-language/nomi/lib/storage/s3 => ../s3

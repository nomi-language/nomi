# minio

Tests of [`lib/storage/s3`](../s3) against a real S3-protocol server. It is
not a module to depend on.

`nomi test lib/storage/minio` starts a throwaway MinIO server
(`minio server`) on a free local port over a temporary directory, creates a
bucket with a signed request, and runs std/storage/testing's conformance
suite, a presigned GET and PUT fetched with no credentials, and an object
read with none from a bucket whose policy makes it public. The server
stops and its directory is removed when the test ends.

It needs `minio` on `PATH`. Without it the test fails, since a Nomi test
cannot skip itself; `TestLibModules` (`cmd/nomi`) skips this project
instead. That is why these tests are a project of their own, apart from
s3's, which need no server.

`real.nomi` runs the same checks, except the public object, which would
need a bucket policy, against any bucket the environment names. It writes
objects under `conformance/` and `presigned/` in that bucket.

```
NOMI_S3_TEST_ENDPOINT=https://nyc3.digitaloceanspaces.com \
NOMI_S3_TEST_BUCKET=my-test-bucket \
NOMI_S3_TEST_ACCESS_KEY=... NOMI_S3_TEST_SECRET_KEY=... \
nomi run lib/storage/minio/real.nomi
```

`NOMI_S3_TEST_REGION` sets the region, `us-east-1` unless given.

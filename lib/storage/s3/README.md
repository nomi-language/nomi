# s3

A `std/storage` backend for any server that speaks the S3 protocol:
DigitalOcean Spaces, MinIO, Cloudflare R2, Backblaze B2 and AWS.
`s3.store(config)` returns a `Store`, std/storage's interface, so code
written against `Store` runs on it unchanged. It is Nomi over `std/http`,
with SigV4 signing over `std/hash`, and has no Go code of its own.

```
nomi add github.com/nomi-language/nomi/lib/storage/s3
```

This `boot` stores files in a Spaces bucket in production and in a local
directory elsewhere:

```nomi
import {
    std::storage.{self, Store}
    std::url.Url
    s3
}

fn boot(startup: Startup) -> Result<App, String> {
    env = startup.env

    store = if Map.get(env, "ENVIRONMENT") == Some("prod") {
        s3.store(s3.Config{
            endpoint: Url`https://nyc3.digitaloceanspaces.com`,
            bucket: "photos",
            credentials: s3.Credentials{
                access_key: Map.get(env, "SPACES_ACCESS_KEY") |> Maybe.with_default(""),
                secret_key: Map.get(env, "SPACES_SECRET_KEY") |> Maybe.with_default(""),
            },
            acl: Some("public-read"),
            cache_control: Some("public, max-age=31536000, immutable"),
        })
        |> Result.map_err(fn e -> "${e}")?
    } else {
        storage.local("tmp/files", public_base: Some(Url`/files/`))
    }

    Ok(App{store})
}
```

- `s3.store(config)` checks the config and sends nothing. An endpoint
  with a path, a bucket name S3 refuses, an empty key and the like are
  `Err(.Backend{code: "InvalidConfig", ...})`. Code that takes a `Store`
  runs the same against `storage.local` in development and
  `std/storage_testing`'s `Memory` in tests.
- `region` defaults to `us-east-1`, which suits MinIO and Spaces; R2 wants
  `auto`, and AWS and B2 the bucket's own region.
- Requests are path-style (`https://<endpoint>/<bucket>/<key>`) unless
  `addressing: Addressing.VirtualHost`, and sign the body's SHA-256 unless
  `payload: Payload.Unsigned`.
- Each attempt is bounded by `timeout` (30 seconds), and a call that fails
  with `Unavailable` is tried again `retries` times (2).
- `Store.list` is `ListObjectsV2`, read with `std/xml`: a prefix, a
  delimiter for common prefixes, a page of up to 1,000 keys (`max-keys`,
  which counts common prefixes too) and the server's continuation token
  as the page's `next`.
- `s3.presign_get` and `s3.presign_put` make URLs that read or write one
  object for up to 7 days with no credentials. `s3.sign` and `s3.presign`
  sign a request this module does not make, such as `CopyObject`.

## Not built

- Multipart uploads and streaming.
- Session tokens and AWS configuration: it takes a static key pair and an
  explicit endpoint.
- Bucket management.

## Tests

`nomi test lib/storage/s3` needs no server: it checks AWS's published SigV4
examples byte for byte, and the requests the store sends, with
`std/http_testing`. [`lib/storage/minio`](../minio) runs std/storage's
conformance suite, presigned URLs and a public object against a throwaway
MinIO server it starts, and its `real.nomi` runs the same checks against
any bucket the environment names.

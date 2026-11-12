# password_hashers

Argon2id and bcrypt for `std/passwords`, over
[golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto). Both are
`PasswordHasher`s, so `passwords.hash` makes their hashes and
`passwords.verify` checks them beside std's PBKDF2 ones.

```
nomi add github.com/nomi-language/nomi/lib/password_hashers
```

```nomi
import
    password_hashers.{Argon2id, Bcrypt}
    std::passwords.{self, PasswordHasher, Pbkdf2}
end

fn hashers() -> List<PasswordHasher>
    [Argon2id{}, Bcrypt{}, Pbkdf2{}]
end

fn sign_in(password: String, stored: String) -> Result<Maybe<String>, String>
    if passwords.verify(password, stored, hashers())? then
        // A new hash to store when this one was made another way.
        if passwords.needs_rehash(stored, hasher: Argon2id{}) then
            Ok(Some(passwords.hash(password, hasher: Argon2id{})?))
        else
            Ok(None)
        end
    else
        Err("wrong password")
    end
end
```

A release `nomi` carries the module's Go code prebuilt, so a program that
uses it needs no Go toolchain while the module is at the release's commit,
which `nomi add` takes and `nomi update` restores. Otherwise the Go code is
built into the program's runner the first time a program that uses it
runs, with the `go` on `PATH` or the Go toolchain `nomi` downloads.

## Argon2id

`Argon2id{}` uses the parameters OWASP's Password Storage Cheat Sheet
gives first: 19 MiB of memory (`memory_kib: 19_456`), 2 passes, 1 lane,
with a 16-byte random salt and a 32-byte hash. It writes and reads the
reference implementation's strings,
`$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>`.

## bcrypt

`Bcrypt{}` reads `$2a$`, `$2b$` and `$2y$` hashes, for passwords an older
system stored, and writes `$2a$` at cost 12. bcrypt reads only a
password's first 72 bytes, so hashing a longer one is an `Err` rather than
a hash that ignores the rest.

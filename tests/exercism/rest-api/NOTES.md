# REST API

Subject: Parsing & Validation

Track IOUs between roommates behind a small JSON API: list users, add one, and record a loan, keeping each user's debts and balance.

Adapted from Exercism problem-specifications (MIT), exercises/rest-api.

## Choices

- Every request and answer is JSON text read and written with std/json. The shapes are structs with `derive ToJson` and `derive FromJson`: `User`, `Users` (the `{"users": [...]}` of the database and of the answers), and the three payloads, `UserNames`, `NewUser` and `Iou`, which stay private. `User`'s `owes`, `owed_by` and `balance` have defaults, so a new user is `#User{name: request.user}`.
- `RestApi` is a value, an opaque struct of the users by name. `RestApi.new(database)` reads the database; `RestApi.get(api, url, payload)` answers the text; `RestApi.post(api, url, payload)` answers the text and the tracker after the request, since nothing changes in place.
- Each failure is an `Error` variant: JSON that is not of the expected shape (with std/json's message), an unknown URL, an unknown user, and a user added twice. The canonical data has no failing case.
- A loan is kept as one number per pair: what the borrower owes the lender, less what the lender already owed the borrower, plus the new amount. `with_net` writes that number into one side of each user's maps, or neither when it is zero, and works the balance out again from the maps, so the canonical "lender owes borrower" cases need no case analysis.
- `read` and `write` are the two steps between text and a typed value: `Json.decode` and `T.from_json`, and `ToJson.to_json` and `Json.encode`.
- The tests compare answers as values, not text. `Json.encode` writes `Float(3.0)` as `3`, and the canonical data writes `3.0`; the two are the same JSON number, so each answer and each expected text are read back into `Users` or `User` and compared with `==`. The JSON is written in backtick raw strings, so its quotes need no escapes.
- The canonical "user management" group is a `group` of three tests, since each case sends a different request; the six "iou" cases are one `subtests` table. A `subtests` row cannot `try`, so the table takes each step apart with `assert Ok(...) = ...`.

## Friction

- [stdlib] std/json has no one call from JSON text to a typed value. `Json.decode` answers a `Json` tree or a `Json.DecodeError`, and `T.from_json` a `T` or a `Json.ShapeError`, two error types with nothing in common, so the solution and the tests each write a generic `read`/`decoded` that runs both and turns each error into text. std has the same function privately, for exported functions (`export_argument`). Rust's `serde_json::from_str::<T>` and Go's `json.Unmarshal` are one call with one error.

# markdown

Markdown for Nomi's `std/html`, over
[goldmark](https://github.com/yuin/goldmark): `markdown.render(text)` is
`text` rendered as HTML, an `Html` a template inserts as it is.

```
nomi add github.com/nomi-language/nomi/lib/markdown
```

```nomi
import {
    std/html.Html
    markdown
}

fn post(title: String, body: String) -> Html {
    Html"""
        <article>
          <h1>${title}</h1>
          ${markdown.render(body)}
        </article>
        """
}
```

The module's Go code is built into the program's runner the first time a
program that uses it runs, with the `go` on `PATH` or the Go toolchain
`nomi` downloads.

## What it renders

CommonMark, with GitHub's extensions: tables, strikethrough, task lists, and
a bare URL as a link (`https://example.com` in a sentence).

## Why its output is trusted

`render` answers its markup through `Html.trusted`, the one way into an
`Html` that skips a template's escaping. It can, because goldmark's safe
defaults are kept:

- Text is escaped: `a < b` renders as `a &lt; b`.
- Raw HTML in the text is left out, written as `<!-- raw HTML omitted -->`.
- A link or image whose URL has a dangerous scheme (`javascript:`) gets an
  empty URL.

So Markdown a user wrote renders safely. There is no option to let raw HTML
through.

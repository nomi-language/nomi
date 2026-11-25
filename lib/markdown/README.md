# markdown

Markdown for Nomi's `std/html`, written in Nomi: `markdown.render(text)` is
`text` rendered as HTML, an `Html` a template inserts as it is.

```sh
nomi add github.com/nomi-language/nomi/lib/markdown
```

A release `nomi` adds it with that command; before the first release, or with a
`nomi` built from source, add it by its directory in a checkout of the
repository: `nomi add ../nomi/lib/markdown`.

```nomi
import
    std::html.Html
    markdown
end

fn post(title: String, body: String) -> Html
    Html"""
        <article>
          <h1>${title}</h1>
          ${markdown.render(body)}
        </article>
        """
end
```

The package is Nomi alone, and every function it exports is pure.

## What it renders

CommonMark 0.31.2, with GitHub's extensions: tables, strikethrough, task
lists, and a bare URL or email address as a link (`https://example.com` in a
sentence). Headings get no `id`, and there are no footnotes.

Its output is goldmark's, byte for byte: goldmark v1.8.7 with its GFM
extension rendered every fixture under `testdata/`, and the tests hold this
package to those renderings. They are the CommonMark spec's examples,
goldmark's own extension cases, cases written for this package, and
documents from this repository. `testdata/NOTICE.txt` says where each comes
from.

## Why its output is trusted

`render` answers its markup through `Html.trusted`, the one way into an
`Html` that skips a template's escaping. It can, because of three rules:

- Text is escaped: `a < b` renders as `a &lt; b`.
- Raw HTML in the text is left out, written as `<!-- raw HTML omitted -->`.
- A link or image whose URL has a dangerous scheme (`javascript:`,
  `vbscript:`, `file:`, and `data:` other than a PNG, GIF, JPEG or WebP
  image) gets an empty URL.

So Markdown a user wrote renders safely. There is no option to let raw HTML
through.

## Speed

On the tour's Markdown it renders about 0.9 MB a second, about 70 times
slower than goldmark compiled: a 5 KB post in about 5 ms. Inputs built to
make a parser quadratic (long runs of `*`, `[` or backticks, unclosed
titles and comments, deep nesting) take time linear in their length, which
`pathological_test.nomi` checks.

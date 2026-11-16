---
title: Nomi Lang
description: A friendly, statically typed language that catches mistakes before your program runs.
# The splash template gives the home page the whole width: no sidebar and no
# table of contents. Every other page keeps both, with Home first in the
# sidebar.
template: splash
next: false
prev: false
# The wordmark carries the NAME, so the <h1> carries the PITCH rather than
# repeating it: a mark reading "NOMI" above a heading reading "Nomi Lang" is
# the same word twice. `title` above still supplies the browser tab, the
# sidebar label and the image's alt text, so the name is not lost anywhere
# the mark cannot be seen.
hero:
  title: "A friendly language that catches mistakes before your program runs."
  tagline: "Statically typed, with values that never change. No null, no exceptions, no forgotten case."
  image:
    alt: Nomi Lang
    light: ../../assets/nomi-wordmark.svg
    dark: ../../assets/nomi-wordmark-dark.svg
  # The three ways in, one per reader: new to programming, new to Nomi,
  # and coming from a language they know. Install is for a reader already
  # convinced.
  actions:
    - text: First Steps
      link: /learn/values/
      variant: primary
    - text: Language Tour
      link: /introduction/
      variant: secondary
    - text: Coming from…
      link: /coming-from/
      variant: secondary
    - text: Install
      link: /tooling/install/
      variant: minimal
---

<!-- The home page's sections are plain HTML around Markdown, styled in
tour.css under "Home page". A blank line after each opening tag lets the
Markdown inside it render, the live example included. -->

<div class="nomi-home-showcase">

<div class="nomi-home-showcase-text">

<p class="nomi-home-kicker">Try it</p>

## Mistakes caught before it runs

`Map.get` can't promise an age, since the name might not be in the map, so
it gives back a `Maybe`: `Some(age)` or `None`.

Delete the `None` line. Nomi won't run the program, and says the `case` is
missing `None`. Every program gets that check, before any of it runs.

Every example on this site runs in your browser, and runs again as you
edit it.

</div>

```nomi-run
fn age_of(name: String) -> String
    ages = {"Ada" => 36, "Bo" => 7}

    case Map.get(ages, name)
        Some(age) -> "${name} is ${age}"
        None      -> "no age for ${name}"
    end
end

fn main()
    dbg age_of("Ada")
    dbg age_of("Cy")
end
```
<!-- expect
dbg line 11: age_of("Ada") = "Ada is 36"
dbg line 12: age_of("Cy") = "no age for Cy"
-->

</div>

<div class="nomi-home-section">

<p class="nomi-home-kicker">The language</p>

## At a glance

<div class="nomi-home-features">
<div class="nomi-home-feature">
<h3>No surprises at run time</h3>
<p>No null. A lookup that can miss gives back a <code>Maybe</code>, a <code>case</code> handles every case, and <code>/</code> won't divide by an Int that might be zero. <a href="/pattern-matching/#what-can-fail-at-run-time">What can still stop a program</a>.</p>
</div>
<div class="nomi-home-feature">
<h3>Values never change</h3>
<p>A value you hold can't change behind your back. To “change” one, you make a new one.</p>
</div>
<div class="nomi-home-feature">
<h3>Pipes</h3>
<p><code>prices |> Iter.filter(on_sale) |> Iter.sum()</code> reads in the order the steps happen.</p>
</div>
<div class="nomi-home-feature">
<h3>Tests beside the code</h3>
<p><code>test</code> blocks are ordinary Nomi, next to what they test, and <code>nomi test</code> runs them.</p>
</div>
<div class="nomi-home-feature">
<h3>One file to ship</h3>
<p><code>nomi build</code> writes one executable that needs nothing installed, for your platform or another.</p>
</div>
<div class="nomi-home-feature">
<h3>Go when you need it</h3>
<p>A program can <a href="/calling-go/">call Go libraries</a> for what the standard library doesn't have.</p>
</div>
</div>

</div>

<div class="nomi-home-section">

<p class="nomi-home-kicker">Where to start</p>

## Pick your way in

<div class="nomi-home-paths">
<a class="nomi-home-path is-primary" href="/learn/values/">
<span class="nomi-home-path-for">New to programming, or to types</span>
<span class="nomi-home-path-name">First Steps</span>
<span class="nomi-home-path-what">Seventeen short lessons from the very start, each with a task you check in the page. Good too if types, values that never change or <code>Maybe</code> are new to you.</span>
</a>
<a class="nomi-home-path" href="/introduction/">
<span class="nomi-home-path-for">You program already</span>
<span class="nomi-home-path-name">Language Tour</span>
<span class="nomi-home-path-what">The whole language, a chapter at a time, every example runnable.</span>
</a>
<a class="nomi-home-path" href="/coming-from/">
<span class="nomi-home-path-for">You know a language well</span>
<span class="nomi-home-path-name">Coming from…</span>
<span class="nomi-home-path-what">Rust, Go, Elixir, TypeScript, Python, Kotlin, Swift or Ruby, mapped onto Nomi.</span>
</a>
</div>

<p class="nomi-home-aside"><a href="/tooling/install/">Install Nomi</a> to run it on your own machine. The source is on <a href="https://github.com/nomi-language/nomi">GitHub</a>.</p>

</div>

<div class="nomi-home-section">

:::note[Design Status]
Nomi is very early. This site is the main place to try the language while the
design is still being worked out. Expressions, types, pattern matching,
imports, interfaces, derives, tests, collections and control flow are the most
used and tested parts. Concurrency, calling Go, and module management are
newer.
:::

</div>

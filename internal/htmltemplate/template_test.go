package htmltemplate

import (
	"reflect"
	"strings"
	"testing"
)

// split turns a template written with `@` for each slot into its parts.
func split(tmpl string) []string { return strings.Split(tmpl, "@") }

func TestSlotContexts(t *testing.T) {
	cases := []struct {
		tmpl string
		want []Context
	}{
		{`<p>@</p>`, []Context{ContextText}},
		{`@`, []Context{ContextText}},
		{`<ul>@</ul>@`, []Context{ContextText, ContextText}},
		{`<title>@ - site</title>`, []Context{ContextRCDATA}},
		{`<textarea name="c">@</textarea>`, []Context{ContextRCDATA}},
		// An SVG <title> is ordinary content, not RCDATA.
		{`<svg><title>@</title></svg>`, []Context{ContextText}},
		{`<div class="a @ b">x</div>`, []Context{ContextAttr}},
		{`<div class='a @ b'>x</div>`, []Context{ContextAttr}},
		{`<div data-reaction="@"></div>`, []Context{ContextAttr}},
		{`<div data-reaction='@'></div>`, []Context{ContextAttr}},
		{`<a href="@">x</a>`, []Context{ContextURLStart}},
		{`<a href='@'>x</a>`, []Context{ContextURLStart}},
		{`<a href=" @">x</a>`, []Context{ContextURLStart}},
		{`<a href="/posts/@">x</a>`, []Context{ContextURLPath}},
		{`<a href='/posts/@'>x</a>`, []Context{ContextURLPath}},
		{`<a href="/posts/@/edit">x</a>`, []Context{ContextURLPath}},
		{`<a href="comment-@">x</a>`, []Context{ContextURLPath}},
		{`<a href="mailto:@">x</a>`, []Context{ContextURLPath}},
		{`<a href="@@">x</a>`, []Context{ContextURLStart, ContextURLPath}},
		{`<a href="/s?q=@">x</a>`, []Context{ContextURLQuery}},
		{`<a href='/s?q=@&amp;p=@'>x</a>`, []Context{ContextURLQuery, ContextURLQuery}},
		{`<a href="/s#@">x</a>`, []Context{ContextURLFragment}},
		{`<a href='/s?q=1#@'>x</a>`, []Context{ContextURLFragment}},
		{`<a href="https://example.com/@">x</a>`, []Context{ContextURLPath}},
		{`<form action="/posts/@/comments" method="POST"></form>`, []Context{ContextURLPath}},
		{`<img src='@' alt="@"/>`, []Context{ContextURLStart, ContextAttr}},
		{`<svg><use xlink:href="@"></use></svg>`, []Context{ContextURLStart}},
		{`<object data="@"></object>`, []Context{ContextURLStart}},
		{`<div data="@"></div>`, []Context{ContextAttr}},
		{`<div style="width: @%"></div>`, []Context{ContextCSS}},
		{`<div style='width: @%'></div>`, []Context{ContextCSS}},
		{`<input type="checkbox" checked=@/>`, []Context{ContextOptionalAttribute}},
		{`<input checked=@ disabled=@>`, []Context{ContextOptionalAttribute, ContextOptionalAttribute}},
		{`<option value="a" selected=@>A</option>`, []Context{ContextOptionalAttribute}},
		{`<button title=@>x</button>`, []Context{ContextOptionalAttribute}},
		{`<p class="x">@ and @</p>`, []Context{ContextText, ContextText}},
		{`<!-- a comment --><p>@</p>`, []Context{ContextText}},
		{`<script src="/a.js"></script><p>@</p>`, []Context{ContextText}},
		{`<p>a < b, @</p>`, []Context{ContextText}},
	}
	for _, tc := range cases {
		tmpl, errs := Parse(split(tc.tmpl), Options{})
		if len(errs) > 0 {
			t.Errorf("%s: the template is refused: %v", tc.tmpl, errs)
			continue
		}
		var got []Context
		for _, s := range tmpl.Slots {
			got = append(got, s.Context)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: contexts %v, want %v", tc.tmpl, got, tc.want)
		}
	}
}

// An optional attribute's name, the space before it and its `=` leave the
// static text for the slot, which writes the attribute or nothing.
func TestOptionalAttributeMovesItsNameIntoTheSlot(t *testing.T) {
	tmpl, errs := Parse(split(`<input type="checkbox" checked=@/><button title=@ class="b">x</button>`), Options{})
	if len(errs) > 0 {
		t.Fatalf("refused: %v", errs)
	}
	want := []string{`<input type="checkbox"`, `/><button`, ` class="b">x</button>`}
	if !reflect.DeepEqual(tmpl.Statics, want) {
		t.Fatalf("statics %q, want %q", tmpl.Statics, want)
	}
	if tmpl.Slots[0].Attr != "checked" || tmpl.Slots[1].Attr != "title" {
		t.Fatalf("attrs %+v", tmpl.Slots)
	}
}

// Static text other than an optional attribute's is emitted as written.
func TestStaticsAreTheParts(t *testing.T) {
	parts := split("<div class='a @'>\n  <p>@</p>\n</div>\n")
	tmpl, errs := Parse(parts, Options{})
	if len(errs) > 0 {
		t.Fatalf("refused: %v", errs)
	}
	if !reflect.DeepEqual(tmpl.Statics, parts) {
		t.Fatalf("statics %q, want %q", tmpl.Statics, parts)
	}
}

func TestAccepted(t *testing.T) {
	for _, tmpl := range []string{
		``,
		`plain text`,
		`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"/><title>t</title></head><body></body></html>`,
		"\n  <!doctype html>\n<html></html>",
		`<br><hr/><img src="/a.png" alt="">`,
		`<svg viewBox="0 0 20 20"><path d="M0 0"/><circle cx="1" cy="1" r="1"></circle></svg>`,
		`<svg><linearGradient id="g"></linearGradient><foreignObject></foreignObject></svg>`,
		`<math><mi>x</mi></math>`,
		`<el-dropdown><el-menu></el-menu></el-dropdown>`,
		`<template><div></div></template>`,
		`<script>if (a < b && c > d) { x = "</p>" }</script>`,
		`<style>p > a { color: red }</style>`,
		`<input required disabled/>`,
		`<input required>`,
		`<div data-action="submit->form-submit#submit"></div>`,
		`<P CLASS="x"></p>`,
		`<textarea><p>not markup</p></textarea>`,
		`<a href='/x?y="z"'>q</a>`,
	} {
		if _, errs := Parse(split(tmpl), Options{}); len(errs) > 0 {
			t.Errorf("%q is refused: %v", tmpl, errs)
		}
	}
}

func TestRefused(t *testing.T) {
	cases := []struct{ tmpl, msg string }{
		{`<div>`, "`<div>` is not closed in this literal"},
		{`<div><p>x</div>`, "`</div>` closes `<p>`, opened at line 1"},
		{"<div>\n<p>\nx</span>", "`</span>` closes `<p>`, opened at line 2"},
		{`</div>`, "`</div>` closes nothing"},
		{`<div/>`, "a `/>` does not close `<div>` in HTML; write `</div>`"},
		{`<dvi></dvi>`, "unknown element `<dvi>`; a custom element's name has a hyphen"},
		{`<br></br>`, "`<br>` has no end tag: it is a void element"},
		{`<p>x</p><!DOCTYPE html>`, "`<!DOCTYPE html>` comes first in a literal"},
		{`<!DOCTYPE foo>`, "the only doctype is `<!DOCTYPE html>`"},
		{`<!-- open`, "a comment is not closed in this literal"},
		{`<a href="x`, "the value of `href` is not closed: end it with `\"`"},
		{`<a href='x`, "the value of `href` is not closed: end it with `'`"},
		{`<div class="a"id="b"></div>`, "put a space before the attribute `id` in `<div>`"},
		{`<?xml version="1.0"?>`, "`<?` is not HTML"},
		{`<![CDATA[x]]>`, "`<!` begins only a comment"},
		{`<input value=>`, "`value=` has no value"},
		{`<a></a b>`, "an end tag has no attributes: write `</a>`"},
		// Slots where no escaping is safe.
		{`<@></div>`, "a slot cannot name a tag or an attribute: `<${tag}>`"},
		{`<div @></div>`, "a slot cannot name a tag or an attribute"},
		{`<div @="x"></div>`, "a slot cannot name a tag or an attribute"},
		{`</@>`, "a slot cannot name a tag or an attribute"},
		{`<div class=a@></div>`, "quote the attribute's value to put a slot in it: `class=\"...${...}...\"`"},
		{`<input checked=@x>`, "quote the attribute's value to put a slot in it: `checked=\"...${...}...\"`"},
		{`<input checked=@@>`, "quote the attribute's value to put a slot in it"},
		{`<button onclick="go(@)">x</button>`, "a slot in `onclick` would be JavaScript; pass the value in a `data-` attribute and read it in a script"},
		{`<button onclick='go(@)'>x</button>`, "a slot in `onclick` would be JavaScript"},
		{`<button onclick=@>x</button>`, "a slot in `onclick` would be JavaScript"},
		{`<script>var x = @</script>`, "a slot in a `<script>` element would be JavaScript; pass the value in a `data-` attribute"},
		{`<style>p { color: @ }</style>`, "a slot in a `<style>` element would be CSS"},
		{`<!-- @ -->`, "a slot in an HTML comment is not rendered safely"},
		{`<img srcset="@ 2x"/>`, "a slot in `srcset` is not supported; build the attribute from `SafeUrl` values"},
		{`<img srcset='@ 2x'/>`, "a slot in `srcset` is not supported"},
		{`<a href="https://@/">x</a>`, "a slot in a URL's scheme or host cannot be escaped: `href=\"https://${host}/\"`"},
		{`<a href='https://@/'>x</a>`, "a slot in a URL's scheme or host cannot be escaped"},
		{`<a href="https://cdn.@">x</a>`, "a slot in a URL's scheme or host cannot be escaped"},
		{`<a href="//@">x</a>`, "a slot in a URL's scheme or host cannot be escaped"},
		{`<a href=@>x</a>`, "quote `href`'s value to put a slot in it: `href=\"${...}\"`"},
		{`<div style=@></div>`, "quote `style`'s value to put a slot in it"},
		{`<!DOCTYPE @>`, "the only doctype is `<!DOCTYPE html>`"},
	}
	for _, tc := range cases {
		_, errs := Parse(split(tc.tmpl), Options{})
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, tc.msg) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want an error containing %q, got %v", tc.tmpl, tc.msg, errs)
		}
	}
}

// A slot's error is at the slot: the end of the part before it.
func TestSlotErrorIsAtTheSlot(t *testing.T) {
	_, errs := Parse(split(`<p>ok</p><script>@</script>`), Options{})
	if len(errs) != 1 || errs[0].Pos != (Pos{Part: 0, Offset: len(`<p>ok</p><script>`)}) {
		t.Fatalf("errors %+v", errs)
	}
}

// Options.Line names the opener's source line.
func TestOpenedAtUsesTheLineOption(t *testing.T) {
	_, errs := Parse(split(`<p>x</div>`), Options{Line: func(Pos) int { return 40 }})
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "opened at line 40") {
		t.Fatalf("errors %+v", errs)
	}
}

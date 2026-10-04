// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import rehypeGoShiki from './src/lib/rehype-go-shiki.mjs';

const referenceItems = [
	{ label: 'Standard Library', link: '/reference/' },
	...[
		'add',
		'assertions',
		'bool',
		'bytes',
		'calendar',
		'channels',
		'codepoints',
		'comparable',
		'compiler',
		'context',
		'debug',
		'decimal',
		'discrete',
		'display',
		'divide',
		'duration',
		'dynamic',
		'equatable',
		'float',
		'hashable',
		'instant',
		'int',
		'io',
		'iter',
		'json',
		'lists',
		'literals',
		'maps',
		'maybe',
		'multiply',
		'random',
		'ranges',
		'regex',
		'results',
		'sets',
		'startup',
		'steppable',
		'strings',
		'structs',
		'subtract',
		'supervisors',
		'tasks',
		'testing',
		'timer',
		'toml',
		'type',
		'unit',
		'vectors',
	].map((name) => ({ label: `std/${name}`, link: `/reference/${name}/` })),
];

// The public origin the site is served from. Starlight registers
// `@astrojs/sitemap` for us, and that integration *skips itself with only a
// build warning* when `site` is unset — which is why the deployed site had no
// `sitemap.xml`. It is also what makes canonical `<link rel="canonical">` and
// the sitemap's `<loc>` entries absolute.
//
// The tour is served at nomi-lang.org. Override it at build time to build for
// another origin, such as a preview deployment:
//
//   SITE_URL=https://example.org make deploy-tour
const site = process.env.SITE_URL || 'https://nomi-lang.org';

// https://astro.build/config
export default defineConfig({
	site,
	markdown: {
		// Code blocks arrive as plain <pre><code class="language-nomi">; our client
		// enhancer (/nomi/tour-client.mjs) highlights them (web-tree-sitter +
		// analyzer, matching Zed) and turns complete programs into run-on-type
		// editors. All served statically from public/nomi/ — no Vite/wasm bundling.
		syntaxHighlight: false,
		rehypePlugins: [rehypeGoShiki],
	},
	integrations: [
		starlight({
			// `title` stays even though the wordmark replaces it visually. It is
			// the page <title>, the mobile menu label, and — verified in the built
			// HTML — the `sr-only` span Starlight puts inside the header link, so
			// the home link keeps an accessible name while the two <img>s are
			// correctly decorative with an empty alt.
			title: 'Nomi Lang',
			logo: {
				// TWO FILES IN src/assets/, which is where Starlight's own docs and
				// starter put a logo. `src/` rather than `public/` because Astro
				// content-hashes what it imports and fails the BUILD on a bad
				// path, where a `public/` URL would 404 silently at runtime — and
				// because a file under `public/` that is also imported gets
				// emitted twice, once verbatim and once hashed.
				//
				// Two files at all because Starlight renders the logo as an
				// <img>, and an <img>-loaded SVG is an isolated document: it
				// cannot read `currentColor` or this page's `data-theme`, so one
				// asset cannot follow the light/dark toggle. Starlight's
				// light/dark pair is the supported way to do it.
				//
				// nomi-wordmark.svg is the master, drawn against One Light — its
				// #242529 and #5b79e3 are exactly tour.css's light
				// `--sl-color-white` and `--sl-color-text-accent`.
				// nomi-wordmark-dark.svg is derived from it by remapping only
				// those two fills to their dark values; path data is identical.
				light: './src/assets/nomi-wordmark.svg',
				dark: './src/assets/nomi-wordmark-dark.svg',
				replacesTitle: true,
			},
			description: 'A guided, runnable introduction to the Nomi language.',
			expressiveCode: false, // our client enhancer owns Nomi code blocks
			customCss: ['./src/styles/tour.css'],
			head: [
				{ tag: 'script', attrs: { type: 'module', src: '/nomi/tour-client.mjs' } },
			],
			sidebar: [
				{
					label: 'Language Tour',
					collapsed: false,
					items: [
						{ label: 'Introduction', link: '/' },
						{ label: 'Bindings & Expressions', link: '/bindings-and-expressions/' },
						{ label: 'Functions & Lambdas', link: '/functions-and-lambdas/' },
						{ label: 'Pipes', link: '/pipes/' },
						{ label: 'Scalars & Strings', link: '/scalars-and-strings/' },
						{ label: 'Collections', link: '/collections/' },
						{ label: 'Iteration & Loops', link: '/iteration-and-loops/' },
						{ label: 'Structs, Enums, Distinct Types', link: '/structs-enums-distinct/' },
						{ label: 'Pattern Matching, Maybe, Result & Try', link: '/pattern-matching/' },
						{ label: 'Testing', link: '/testing/' },
						{ label: 'Generics', link: '/generics/' },
						{ label: 'Interfaces & Dispatch', link: '/interfaces-and-dispatch/' },
						{ label: 'Modules & Imports', link: '/modules-and-imports/' },
						{ label: 'App Fields, Defer & Context', link: '/capabilities-and-context/' },
						{ label: 'Concurrency', link: '/concurrency/' },
						{ label: 'Dates & Times', link: '/dates-and-times/' },
						{ label: 'Typed Literals', link: '/typed-literals/' },
						{ label: 'FFI & Dynamic', link: '/ffi-and-dynamic/' },
					],
				},
				// Generated by cmd/nomi-docgen from the stdlib's /// doc comments.
				{
					label: 'Standard Library',
					collapsed: true,
					items: referenceItems,
				},
			],
		}),
	],
});

import { codeToHast } from "shiki";

function textContent(node) {
  if (!node) return "";
  if (node.type === "text") return node.value || "";
  if (!node.children) return "";
  return node.children.map(textContent).join("");
}

function hasClass(node, cls) {
  const props = node?.properties || {};
  const classes = props.className || props.class || [];
  return Array.isArray(classes) ? classes.includes(cls) : String(classes).split(/\s+/).includes(cls);
}

function addClass(node, cls) {
  const props = node.properties || {};
  const classes = props.className || props.class || [];
  const list = Array.isArray(classes) ? classes : String(classes).split(/\s+/).filter(Boolean);
  if (!list.includes(cls)) list.push(cls);
  node.properties = { ...props, className: list.join(" ") };
}

function setCodeLabel(pre, label) {
  addClass(pre, `nomi-code-${label.toLowerCase()}`);
}

// Static blocks Shiki highlights at build time, by fence language: the
// shiki grammar and the chip class (`nomi-code-<chip>`) each one gets.
const SHIKI_LANGS = {
  go: { lang: "go", chip: "go" },
  js: { lang: "javascript", chip: "js" },
  javascript: { lang: "javascript", chip: "js" },
};

async function highlight(code, { lang, chip }) {
  const root = await codeToHast(code, {
    lang,
    themes: { light: "one-light", dark: "one-dark-pro" },
  });
  const pre = root.children?.find((node) => node.type === "element" && node.tagName === "pre");
  const codeNode = pre?.children?.find((node) => node.type === "element" && node.tagName === "code");
  return {
    type: "element",
    tagName: "pre",
    properties: { className: `nomi-tour nomi-code-${chip}` },
    children: codeNode?.children || [],
  };
}

export default function rehypeShiki() {
  return async function transform(tree) {
    const edits = [];

    function visit(node, parent) {
      if (!node || !node.children) return;
      for (let i = 0; i < node.children.length; i++) {
        const child = node.children[i];
        if (child?.type === "element" && child.tagName === "pre") {
          const code = child.children?.find((c) => c.type === "element" && c.tagName === "code");
          const fence = Object.keys(SHIKI_LANGS).find((name) => hasClass(code, `language-${name}`));
          if (fence) {
            edits.push({ parent: node, index: i, fence, code: textContent(code).replace(/\n+$/, "") });
          } else if (
            hasClass(code, "language-nomi") ||
            hasClass(code, "language-nomi-run") ||
            hasClass(code, "language-nomi-test")
          ) {
            setCodeLabel(child, "Nomi");
          }
        }
        visit(child, node);
      }
    }

    visit(tree, null);
    for (const edit of edits) {
      edit.parent.children[edit.index] = await highlight(edit.code, SHIKI_LANGS[edit.fence]);
    }
  };
}

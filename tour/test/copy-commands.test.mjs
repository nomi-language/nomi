// A ```console block's Copy button copies its commands alone: each line
// after a prompt (`$ `, or PowerShell's `PS> `) without it, with the lines
// that continue it after a `\`, and none of the output. rehype-shiki.mjs
// decides them and carries them as the block's data-nomi-commands, which
// tour-client.mjs's copyControls copies; a block of output alone carries ""
// and gets no button.
//
//   npm --prefix tour test
import { test } from "node:test";
import assert from "node:assert/strict";
import rehypeShiki from "../src/lib/rehype-shiki.mjs";

async function commandsOf(text) {
  const tree = {
    type: "root",
    children: [
      {
        type: "element",
        tagName: "pre",
        properties: {},
        children: [
          {
            type: "element",
            tagName: "code",
            properties: { className: ["language-console"] },
            children: [{ type: "text", value: text + "\n" }],
          },
        ],
      },
    ],
  };
  await rehypeShiki()(tree);
  const pre = tree.children[0];
  assert.ok(String(pre.properties.className).includes("nomi-console"), "the block renders as a console block");
  return pre.properties.dataNomiCommands;
}

test("a prompt line is copied without its prompt", async () => {
  assert.equal(
    await commandsOf("$ curl -fsSL https://nomi-lang.org/install.sh | sh"),
    "curl -fsSL https://nomi-lang.org/install.sh | sh",
  );
});

test("a PowerShell prompt line is copied without its prompt", async () => {
  assert.equal(
    await commandsOf("PS> irm https://nomi-lang.org/install.ps1 | iex"),
    "irm https://nomi-lang.org/install.ps1 | iex",
  );
});

test("output lines are left out and commands are joined by newlines", async () => {
  const block = [
    "$ nomi run main.nomi",
    "hello",
    "error: div by zero",
    "$ nomi test   # runs every test",
    "test result: ok. 3 passed",
  ].join("\n");
  assert.equal(await commandsOf(block), "nomi run main.nomi\nnomi test   # runs every test");
});

test("a command continued with a trailing backslash keeps its continuation lines", async () => {
  const block = [
    "$ nomi build \\",
    "    --target linux-amd64 \\",
    "    -o app",
    "built app",
    "$ ./app",
    "running",
  ].join("\n");
  assert.equal(await commandsOf(block), "nomi build \\\n    --target linux-amd64 \\\n    -o app\n./app");
});

test("a block of output alone has no command to copy", async () => {
  const block = ["error: unknown name `x`", "  --> main.nomi:3:5", "   |", " 3 |     x", "   |     ^"].join("\n");
  assert.equal(await commandsOf(block), "");
});

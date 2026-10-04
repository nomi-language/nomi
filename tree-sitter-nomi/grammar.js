module.exports = grammar({
  name: 'nomi',

  extras: $ => [
    /\s/,
    $.attached_comment_test_line_prompt,
    $.doc_comment,
    $.line_comment,
  ],

  // External scanner. Pure grammar can't reliably enforce the
  // "interpolation requires `${`" constraint inside strings —
  // tree-sitter's LR parser performs error recovery that treats a
  // bare `{` mid-string as an interpolation start, which
  // diverges from the lexer's actual semantics (bare `{` is plain
  // text — see `lexer/lexer.go`). This scanner emits
  // `string_content` and `triple_string_content` tokens directly,
  // consuming everything up to the relevant boundary chars (`"`,
  // `\`, `${`) and giving the parser no opportunity to recover.
  //
  // Raw backtick forms disable interpolation parsing entirely. Their
  // bodies are emitted by `raw_string_content`, which stops only at
  // the closing backtick (no `${` boundary, no escape processing —
  // backslash is literal).
  externals: $ => [
    $.string_content,
    $.triple_string_content,
    $.raw_string_content,
    // Separator between `case` arms, emitted by the external scanner on a
    // newline immediately followed by `.Uppercase` (a `dot_variant` pattern
    // starting the next arm). Without it a complete arm body (`sort(xs, f)`)
    // greedily extends via `expr.Type` (type_field_access) and swallows the
    // next `.Variant` arm. It fires ONLY before `.Uppercase`, so it never cuts
    // a multi-line body that continues with `|>`, an operator, or a lowercase
    // `.field` — none of those start a dot-variant arm.
    $._case_arm_sep,
    // Opening brace of a headless anonymous-struct / map literal in value or
    // scrutinee position. The dedicated tokens let the parser keep
    // `anon_struct` / `map_literal` separate from block `body` states when the
    // external scanner can classify the brace by content (`IDENT:` → struct,
    // `=>` → map). The grammar also accepts a plain `{` for these rules and
    // resolves the block/literal overlap with explicit conflicts.
    $._anon_struct_open,
    $._map_open,
    // Line-leading attached test prompts. The first prompt opens an attached
    // test node; following body-line prompts are named extras so prompted
    // attached tests keep normal Nomi parsing/highlighting after each `//!`.
    $.attached_comment_test_prompt_marker,
    $.attached_comment_test_line_prompt,
    $._attached_test_prompt_group_end,
  ],

  word: $ => $.identifier,

  conflicts: $ => [
    [$.struct_construction, $._simple_type],
    [$.struct_construction, $.struct_pattern],
    [$.map_construction, $._simple_type],
    [$.map_construction, $.struct_pattern],
    [$.pattern_assertion_binding, $.field_access],
    [$.pattern_assertion_binding, $.assertion, $.field_access],
    [$.pattern_assertion_binding, $.destructuring_binding],
    [$._expression, $._expression_or_statement],
    [$._expression, $._callable_expression],
    [$._expression, $._lambda_body_expression],
    [$._callable_expression, $._lambda_body_expression],
    [$._lambda_body_expression, $._expression, $._callable_expression],
    [$._expression, $.destructuring_binding],
    [$.destructuring_binding, $.struct_pattern],
    [$._expression, $.enum_pattern, $._callable_expression],
    [$.enum_pattern, $._callable_expression],
    [$.list, $.list_pattern],
    [$._expression, $.pattern_field],
    [$.destructuring_binding, $.pattern_field],
    [$.destructuring_binding, $._expression, $.pattern_field],
    [$.body, $.anon_struct],
    // `{..base, f: v}` reaches here through a plain `{` whenever the brace is
    // NOT whitespace-preceded (`f({..base, x: 1})`), because that is the one
    // case `scan_headless_brace` declines to classify. The contents decide:
    // `body` has no production for a leading `..`, so it dies and the update
    // literal wins.
    [$.body, $.struct_update],
    [$.body, $.map_literal],
    [$.module_path],
    [$.lambda_parameter, $._expression],
    // A single `_import_body` covers all import shapes (bare module, single
    // name shorthand `path.Type`, drill-through `path.Type.{...}`, flat
    // selective `path.{...}`). The shorthand-vs-drill and shorthand-vs-flat
    // overlaps share the `module_path .` prefix; GLR resolves them once the
    // token after `.` (a type_identifier vs `{`) and the trailing
    // `.import_names` are seen.
    [$._import_body],
    // `type` declarations vs trailing `.member` lookups; surfaced by the
    // same expanded LR(1) lookahead as the import conflict above.
    [$._simple_type],
    // `case { .Variant [ ... }`: parser can't tell whether dot_variant is
    // a complete case-pattern (followed by a `[...]` list pattern at
    // _case_pattern) or the prefix of a list-variant pattern. Resolved at
    // GLR runtime — both are valid until the closing `]` decides.
    [$._expression, $.list_pattern],
    // `Date"..."` (PascalCase tagged literal): the parser reads a
    // type_identifier and must decide whether to reduce it to a
    // standalone `_expression` (the type) or shift toward
    // `tagged_type_string` expecting an immediate `"`. Snake_case
    // (`sql"..."`) doesn't need this because the lexer scans in
    // no-skip mode after a non-type identifier; declaring conflicts
    // against the upstream rules where the type_identifier is read
    // forces tree-sitter's GLR to keep the tagged_type_string path
    // alive past the type_identifier so the immediate-quote can win.
    [$._expression, $.tagged_type_string],
    [$._expression, $.tagged_type_triple_string],
    [$._expression, $.raw_tagged_string],
    [$._simple_type, $.tagged_type_string],
    [$._simple_type, $.raw_tagged_string],
    [$._callable_expression, $.tagged_type_string],
    [$._callable_expression, $.raw_tagged_string],
    // `instant.Instant` in type position vs `instant.Foo` as an
    // expression-level field access: the parser can't disambiguate
    // from one symbol of lookahead. Where _type is expected
    // (return type, parameter type, …) qualified_type wins; where
    // _expression is expected, type_field_access takes over.
    [$._expression, $.qualified_type],
    [$.enum_variant],
  ],

  rules: {
    source_file: $ => repeat($._item),

    _item: $ => choice(
      $.decorated_item,
      $.derive_conformance,
      $.once_binding,
      $.function_definition,
      $.extern_func_definition,
      $.extern_type_definition,
      $.type_definition,
      $.struct_definition,
      $.enum_definition,
      $.type_alias,
      $.interface_definition,
      $.impl_block,
      $.test_declaration,
      $.tests_declaration,
      $.import_statement,
      $.import_block,
      $.gopkg_declaration,
      $.go_block,
      $._expression_or_statement,
    ),

    // Visibility marker: `pub` makes a top-level declaration part of
    // the module's public surface. Wrapped as an explicit `pub` rule so
    // tree-sitter treats it as an anonymous keyword token (matching
    // `extern`, `once`, `fn`, etc.) rather than a free-standing
    // identifier reachable through `_expression_or_statement`.
    pub: $ => 'pub',

    attached_test_prompt: $ => seq(
      $.attached_comment_test_prompt_marker,
      field('body', repeat1(choice(
        $.import_statement,
        $.import_block,
        $._expression_or_statement,
      ))),
      $._attached_test_prompt_group_end,
    ),

    decorated_item: $ => prec.right(seq(
      repeat1($.attached_test_prompt),
      choice(
        $.once_binding,
        $.function_definition,
        $.extern_func_definition,
        $.extern_type_definition,
        $.type_definition,
        $.struct_definition,
        $.enum_definition,
        $.type_alias,
        $.interface_definition,
        $.impl_block,
      ),
    )),

    _decorated_inherent_item: $ => prec.right(seq(
      repeat1($.attached_test_prompt),
      choice(
        $.once_binding,
        $.function_definition,
        $.extern_func_definition,
      ),
    )),

    // Once binding: once name = expression
    //               once name: T = expression  (type annotation optional)
    //               pub once name [: T] = expression  (publicly exported)
    once_binding: $ => seq(
      optional($.pub),
      'once',
      field('name', choice($.identifier, $.type_identifier)),
      optional(seq(':', field('type', $._type))),
      '=',
      field('value', $._expression),
    ),

    // Import
    //
    // Optional line-level `export [as <name>]` re-exports the imported
    // module/names from this file. Order is: alias first, then export.
    //   import some_mod export
    //   import some_mod as sm export as Public
    //   import some_mod: a, b export
    import_statement: $ => seq('import', $._import_body),

    // Import block: `import { entry entry ... }` — bundles multiple imports
    // into a single statement. Entries use the same `_import_body` shape as a
    // standalone import. Commas appear only inside an entry's selective name
    // list (`mod.{a, b}`).
    import_block: $ => seq(
      'import',
      '{',
      repeat1($._import_body),
      '}',
    ),

    // The body of an import (everything after the `import` keyword), shared by
    // import_statement and import_block. Covers every shape:
    //   path                      (file namespace)
    //   path as alias             (file namespace alias)
    //   path.name                 (single-name selective)
    //   path: Type                (selector-list selective)
    //   path: Owner.Type          (single-name owner selective)
    //   path: Owner.{Names}       (owner selective)
    //   path: {Names}             (flat selective)
    // plus optional `as` alias and `export` re-export. The `.` boundary is the
    // owner/member boundary inside the declaration selector; `/` joins
    // module-path segments inside `module_path`.
    _import_body: $ => seq(
      $.module_path,
      optional(choice(
        $.import_selector,
        seq('.', $.import_value_selector),
        seq(
          optional(seq('.', $.type_identifier)),
          optional(seq('.', $.import_names)),
        ),
      )),
      optional(seq('as', choice($.identifier, $.type_identifier))),
      optional(seq('export', optional(seq('as', choice($.identifier, $.type_identifier))))),
    ),

    module_path: $ => seq(
      $._import_path_segment,
      repeat(seq('/', $._import_path_segment)),
    ),

    _import_path_segment: $ => choice(
      alias('..', $.relative_import_parent),
      $.identifier,
      $.type_identifier,
      alias($._import_path_keyword, $.identifier),
    ),

    _import_path_keyword: _ => token(prec(-1, /and|as|assert|break|case|continue|dbg|defer|else|enum|export|extern|fn|for|if|impl|import|interface|once|opaque|or|pub|refute|return|self|setup|struct|tests?|todo|try|type(alias)?|where|with/)),

    import_selector: $ => prec.right(seq(
      ':',
      commaSep1($.import_selector_clause),
    )),

    import_selector_clause: $ => choice(
      $.import_names,
      prec.right(1, seq(
        choice($.identifier, $.type_identifier),
        repeat(seq('.', choice($.identifier, $.type_identifier))),
        '.',
        $.import_names,
      )),
      prec(-1, $.import_selector_path),
    ),

    import_selector_path: $ => prec.right(seq(
      choice($.identifier, $.type_identifier),
      repeat(seq('.', choice($.identifier, $.type_identifier))),
    )),

    import_value_selector: $ => $.identifier,

    import_names: $ => seq('{', commaSep1(choice($.import_self, $.import_entry)), '}'),

    // `self` inside a selective brace list lifts the LHS of the brace
    // group: `import path.{self, X, Y}` also binds `path` (the module);
    // `import path.Enum.{self, X, Y}` also binds `Enum` (the type).
    // No alias / no export modifier — it's a marker, not a name.
    import_self: $ => 'self',

    // One name in a selective-import brace list:
    //   Name
    //   Name as Local
    //   Name export
    //   Name export as Pub
    //   Name as Local export
    //   Name as Local export as Pub
    // Order is fixed: rename-on-import first, then re-export modifier.
    import_entry: $ => seq(
      choice($.identifier, $.type_identifier),
      repeat(seq('.', choice($.identifier, $.type_identifier))),
      optional(seq('as', choice($.identifier, $.type_identifier))),
      optional(seq('export', optional(seq('as', choice($.identifier, $.type_identifier))))),
    ),

    gopkg_declaration: $ => seq(
      'gopkg',
      field('path', $.string),
      optional(seq(
        'as',
        field('alias', choice($.identifier, '_')),
      )),
    ),

    go_selector_binding: $ => seq(
      'go',
      field('package', choice($.identifier, '_')),
      '.',
      field('symbol', choice($.identifier, $.type_identifier)),
    ),

    // Functions
    function_definition: $ => prec.right(seq(
      optional($.pub),
      optional('impl'),
      'fn',
      choice(
        seq(
          field('impl_interface', choice(
            $.type_identifier,
            seq(choice($.identifier, $.type_identifier), '.', $.type_identifier),
          )),
          '.',
          field('name', choice($.identifier, $.type_identifier)),
        ),
        field('name', choice(
          $.identifier,
          $.type_identifier,
        )),
      ),
      optional($.type_parameters),
      $.parameters,
      optional(seq(':', $._type)),
      optional($.where_clause),
      optional(choice($.go_selector_binding, $.go_inline_body, $.body)),
    )),

    go_block: $ => seq(
      'go',
      '{',
      optional($.go_raw_content),
      '}',
    ),

    go_inline_body: $ => seq(
      'go',
      '{',
      optional($.go_raw_content),
      '}',
    ),

    go_raw_content: $ => repeat1(choice(
      token(prec(-1, /[^{}]+/)),
      seq('{', optional($.go_raw_content), '}'),
    )),

    // Host declarations
    extern_func_definition: $ => prec(1, seq(
      optional($.pub),
      optional('impl'),
      $.host_keyword,
      'fn',
      choice(
        seq(
          field('impl_interface', choice(
            $.type_identifier,
            seq(choice($.identifier, $.type_identifier), '.', $.type_identifier),
          )),
          '.',
          field('name', choice($.identifier, $.type_identifier)),
        ),
        field('name', choice($.identifier, $.type_identifier)),
      ),
      optional($.type_parameters),
      $.parameters,
      optional(seq(':', $._type)),
      optional($.where_clause),
    )),

    // Host type — opaque runtime-provided type:
    //   pub host type Instant
    //   pub host type Int
    // prec.right attaches a following `{` as the item body rather than
    // ending the declaration and parsing a standalone block.
    extern_type_definition: $ => prec.right(1, seq(
      optional($.pub),
      $.host_keyword,
      'type',
      field('name', $.qualified_type_identifier),
      optional($.type_parameters),
      optional($.where_clause),
      optional($.type_body),
    )),

    parameters: $ => seq('(', commaSep($.parameter), ')'),

    host_keyword: _ => 'host',

    // A function-definition parameter is either a plain name or an
    // irrefutable destructuring pattern (tuple, struct/typed-struct,
    // distinct/single-variant-enum), each optionally annotated with a
    // type and/or given a default. The Go parser accepts the same shapes
    // and a self-typing rule lets a type-named pattern omit `: Type`; the
    // grammar only needs to parse the shapes — refutability is the
    // checker's job. The pattern rules are reused from the `case`-arm
    // grammar (`tuple_pattern`/`struct_pattern`/`enum_pattern`).
    parameter: $ => seq(
      field('name', $._param_pattern),
      optional(seq(':', $._type)),
      optional(seq('=', $._expression)),
    ),

    // The destructuring shapes admitted in parameter position (fn + lambda).
    // A bare `_` wildcard and bare `identifier` are the plain-name forms.
    _param_pattern: $ => choice(
      $.identifier,
      '_',
      $.tuple_pattern,
      $.struct_pattern,
      $.enum_pattern,
    ),

    type_parameters: $ => seq('<', commaSep1($.type_parameter), '>'),
    // Generic headers introduce type-parameter names only. Bounds live in a
    // following `where` clause (`fn f<T>(x: T): T where T: Display`).
    type_parameter: $ => seq(
      field('name', $.type_parameter_identifier),
    ),

    qualified_type_identifier: $ => prec.left(seq(
      $.type_identifier,
      repeat(seq('.', $.type_identifier)),
    )),

    // Struct field: `name: Type [= default]`. Fields are newline-separated
    // (no comma), matching the Go parser's parseStructBody.
    field_definition: $ => seq(
      field('name', $._field_identifier),
      ':',
      field('type', $._type),
      optional(seq('=', $._expression)),
    ),

    // Comma'd field, used for struct-shaped enum-variant payloads
    // (`Rect {w: Float, h: Float = 1.0}`). Kept separate from declaration
    // fields so payload fields may carry trailing commas. `prec(3)` outranks
    // `anon_struct_type_field` (prec 2) so the payload-with-default form wins
    // over an anon-struct-type payload (`Name {…}` vs `Name <anon struct
    // type>`).
    variant_field: $ => prec(3, seq(
      $._field_identifier,
      ':',
      $._type,
      optional(seq('=', $._expression)),
      optional(','),
    )),

    // Type — distinct-type wrapper or zero-sized marker:
    //   pub type Foo Inner              → distinct over Inner
    //   pub type Foo (T, U)             → tuple-distinct
    //   pub type Foo                    → zero-sized
    // Struct and enum forms are separate top-level rules
    // (struct_definition, enum_definition).
    //
    // Modifier order: `pub`? `opaque`? `type`. `opaque` is only valid on
    // distinct types (those with an `Inner` representation); the analyzer
    // enforces.
    //
    // The inner type is `_distinct_inner_type` (not the full `_type`):
    // the Go parser only admits TYPE_IDENT / IDENT / LPAREN starts there,
    // which keeps a brace after the name unambiguous — `type Foo {` is
    // always the item body, never an anon-struct-type RHS (top-level
    // `type Foo {a: Int}` is rejected; `struct` owns record types).
    type_definition: $ => prec.right(seq(
      optional($.pub),
      optional('opaque'),
      'type',
      field('name', $.qualified_type_identifier),
      optional($.type_parameters),
      optional(choice(
        $.go_selector_binding,
        $.go_inline_body,
        seq($._distinct_inner_type, optional($.type_body)),
        $.type_body,
      )),
    )),

    // Type and extern-type bodies hold only the type's own qualified API.
    // Fields belong to structs; variants belong to enums; interface impls and
    // derives stay outside the type.
    type_body: $ => seq(
      '{',
      repeat(choice(
        $._decorated_inherent_item,
        $.function_definition,
        $.extern_func_definition,
        $.once_binding,
      )),
      '}',
    ),

    // The type expressions admitted as a distinct type's RHS — everything
    // except `self` and a bare anonymous struct type (whose `{` belongs
    // to the item body instead; anon structs remain reachable in nested
    // positions like tuple elements and generic args).
    _distinct_inner_type: $ => choice(
      $._simple_type,
      $.qualified_type,
      $.generic_type,
      $.function_type,
      $.tuple_type,
    ),

    // Struct — nominal record type with named fields. No space before `{`
    // is a formatter convention, not a grammar requirement.
    struct_definition: $ => seq(
      optional($.pub),
      optional('opaque'),
      'struct',
      field('name', $.qualified_type_identifier),
      optional($.type_parameters),
      optional($.where_clause),
      $.struct_body,
    ),

    // Struct bodies hold newline-separated fields plus the type's own
    // inherent API (`fn` / `once`). Interface impls, derives, and tests stay
    // outside the type.
    struct_body: $ => seq(
      '{',
      repeat(choice(
        $._decorated_inherent_item,
        $.field_definition,
        $.function_definition,
        $.extern_func_definition,
        $.once_binding,
      )),
      '}',
    ),

    // Enum — sum type. Variants are always one per line; no `|` separators.
    enum_definition: $ => seq(
      optional($.pub),
      optional('opaque'),
      'enum',
      field('name', $.qualified_type_identifier),
      optional($.type_parameters),
      optional($.where_clause),
      $.enum_body,
    ),

    enum_body: $ => seq(
      '{',
      repeat(choice(
        $._decorated_inherent_item,
        $.enum_variant,
        $.function_definition,
        $.extern_func_definition,
        $.once_binding,
      )),
      '}',
    ),

    // One enum variant. Payload shapes:
    //   embeds Other            → embedded variant
    //   Name {f: T, g: U = d}   → struct payload (comma'd fields)
    //   Name Type               → positional payload
    //   Name                    → bare
    enum_variant: $ => seq(
      choice(
        seq('embeds', $._type),
        seq(field('name', $.type_identifier), '{', repeat(alias($.variant_field, $.field_definition)), '}'),
        prec.dynamic(1, seq(field('name', $.type_identifier), $._enum_payload_type)),
        field('name', $.type_identifier),
      ),
    ),

    // Type alias — `typealias Name Target` (regular type alias) or
    // `typealias Name A and B` (bound alias, conjunction of interfaces
    // usable only in `where T: Name` positions).
    type_alias: $ => seq(
      optional($.pub),
      'typealias',
      field('name', $.qualified_type_identifier),
      field('type', $._type),
      repeat(seq('and', field('type', $._type))),
    ),

    // Interface
    //
    // Interface bodies hold contract members:
    //   - `interface_method`: plain `fn ...` declarations (signature-only
    //     = required; with a body = default method), optionally prefixed with
    //     the contextual `open` keyword to mark a default as overridable
    //     (without `open`, defaults are final), and optionally `extern`
    //     for host-backed defaults.
    //   - `interface_field`: `field name: T` requirements that any
    //     implementing struct must surface as a real field of the
    //     given type.
    // Both `field` and `open` are CONTEXTUAL keywords in Nomi — they
    // only carry meaning inside an interface body. Because the literal
    // strings appear *only* inside these rules, tree-sitter's keyword
    // extraction won't elevate them to global reserved words; outside
    // an interface body they continue to lex as plain identifiers, so
    // user code with locals/fields named `field` or `open` keeps working.
    interface_definition: $ => seq(
      optional($.pub),
      'interface',
      field('name', $.type_identifier),
      optional($.type_parameters),
      optional(seq('extends', $._type)),
      optional($.where_clause),
      '{',
      repeat(choice(
        $.interface_method,
        $.interface_field,
      )),
      '}',
    ),

    interface_method: $ => seq(
      optional('open'),
      optional('extern'),
      'fn',
      field('name', choice($.identifier, $.type_identifier)),
      optional($.type_parameters),
      $.parameters,
      optional(seq(':', $._type)),
      optional($.where_clause),
      optional($.body),
    ),

    // Function-level `where` clause:
    //   where T: Comparable, K: Hashable and Equatable
    // Constrains a type variable already in scope without re-declaring it. It
    // sits after the return type and before a body brace when a body exists.
    // `where` is a hard keyword.
    where_clause: $ => prec.right(seq(
      'where',
      $.where_constraint,
      repeat(seq(',', $.where_constraint)),
    )),

    // One `Name: Bound [and Bound]*` entry in a where clause. The bound list
    // is conjunctive (AND), the same form used by generic `where` clauses.
    where_constraint: $ => seq(
      field('name', $.type_parameter_identifier),
      ':',
      $._type,
      repeat(seq('and', $._type)),
    ),

    interface_field: $ => seq(
      'field',
      field('name', $.identifier),
      ':',
      field('type', $._type),
    ),

    // Top-level `impl` block. `impl Iface for Type` implements one interface
    // for one type. `impl Type` defines type-qualified functions and
    // type-associated once bindings.
    //   impl Type { ... }
    //   impl Display for Type { ... }
    //   impl<T> Iter for List<T> where T: Bound { ... }
    //   impl Display for Conn
    // `self` is parsed as a type expression inside item signatures so the
    // analyzer can reject concrete uses with a targeted diagnostic.
    impl_block: $ => seq(
      'impl',
      optional($.type_parameters),
      choice(
        prec(1, seq(
          field('interface', $._type),
          'for',
          field('receiver', $._type),
          optional($.where_clause),
          $._impl_body,
        )),
        prec(1, seq(
          field('receiver', $._type),
          optional($.where_clause),
          $._impl_body,
        )),
        seq(
          field('interface', $._type),
          'for',
          field('receiver', $._type),
          optional($.where_clause),
        ),
      ),
    ),

    _impl_body: $ => seq(
      '{',
      repeat(choice($.decorated_impl_item, $.function_definition, $.extern_func_definition, $.once_binding)),
      '}',
    ),

    // `test "name" { }`, or `test "name", pattern { }` binding the group's
    // setup value with any irrefutable pattern (`db`, `(a, b)`, `{a, b}`).
    test_declaration: $ => seq(
      'test',
      field('name', $.string),
      optional(seq(',', field('pattern', $._param_pattern))),
      $.body,
    ),

    tests_declaration: $ => seq(
      'tests',
      field('name', $.string),
      $.tests_body,
    ),

    // A group holds at most one each of its `clock`, `boot` and `setup` lines
    // and its tests, in any order: the lines run in a fixed order whatever
    // their position. The compiler rejects a second `clock`, `boot` or
    // `setup`; the grammar accepts it, so an editor keeps highlighting.
    tests_body: $ => seq(
      '{',
      repeat(choice($.clock_block, $.boot_block, $.setup_block, $.test_declaration)),
      '}',
    ),

    // `clock Clock.Virtual` selects the clock each test runs under.
    clock_block: $ => seq(
      'clock',
      field('clock', $._expression),
    ),

    // `boot server.boot(startup())`: the expression that boots the app each
    // test reads its fields from, normally a call of an entry file's boot.
    boot_block: $ => seq(
      'boot',
      field('boot', $._expression),
    ),

    // `setup expr` or `setup { stmts; value }`: its value is what a test's
    // pattern binds.
    setup_block: $ => seq(
      'setup',
      field('body', $._expression),
    ),

    // Top-level derive declaration. The interface is synthesized
    // structurally by the compiler.
    //   derive Equatable for Point
    //   derive Comparable for Box<T> where T: Comparable
    //   derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}
    derive_conformance: $ => prec.right(seq(
      'derive',
      optional($.type_parameters),
      field('interface', $._type),
      'for',
      field('receiver', $._type),
      optional(seq('with', field('options', $._expression))),
      optional($.where_clause),
    )),

    // One item inside an impl block: a `fn` / `pub fn` / `host fn` /
    // `pub host fn` / `once` / `pub once`.
    _impl_item: $ => choice(
      $.function_definition,
      $.extern_func_definition,
      $.once_binding,
    ),

    decorated_impl_item: $ => prec.right(seq(
      repeat1($.attached_test_prompt),
      $._impl_item,
    )),

    // Types
    _type: $ => choice(
      $.self_type,
      $._simple_type,
      $.qualified_type,
      $.generic_type,
      $.function_type,
      $.tuple_type,
      $.anon_struct_type,
    ),

    // Enum positional payloads (`Some T`) must stay on the same line as the
    // variant name. The leading-space token keeps tree-sitter from treating
    // adjacent bare variants (`A` newline `B`) as one positional variant.
    _enum_payload_type: $ => choice(
      $._enum_payload_simple_type,
      $._enum_payload_generic_type,
      $._enum_payload_function_type,
      $._enum_payload_tuple_type,
      $._enum_payload_parenthesized_type,
    ),

    _same_line_type_identifier: $ => alias(token.immediate(seq(/[ \t]+/, /[A-Z][A-Za-z0-9_]*/)), $.type_identifier),
    _same_line_lparen: $ => token.immediate(seq(/[ \t]+/, '(')),

    _enum_payload_simple_type: $ => prec(1, seq(
      $._same_line_type_identifier,
      repeat(seq(token.immediate('.'), $.type_identifier)),
    )),

    _enum_payload_generic_type: $ => prec(2, seq(
      $._same_line_type_identifier,
      '<',
      commaSep1($._type),
      '>',
    )),

    _enum_payload_function_type: $ => prec.right(3, seq(
      $._same_line_lparen,
      commaSep($._type),
      ')',
      '->',
      $._type,
    )),

    _enum_payload_tuple_type: $ => seq(
      $._same_line_lparen,
      $._type,
      repeat1(seq(',', $._type)),
      optional(','),
      ')',
    ),

    // `Num (Int)` / `Num ((Int) -> Int)`: one payload type in parentheses,
    // which the Go parser reads as the type itself (there are no 1-tuples).
    _enum_payload_parenthesized_type: $ => seq(
      $._same_line_lparen,
      $._type,
      ')',
    ),

    // Module-qualified type: `instant.Instant`, `calendar.Date`. The
    // module prefix is lowercase (identifier); the type is PascalCase
    // (type_identifier). Trailing `.TypeIdentifier` segments cover
    // nested members (e.g. enum variants reached by dot, matching the
    // pattern `Status.Open` would use). Distinct from `_simple_type`
    // (PascalCase-only chain) and from `type_field_access` (which is
    // an expression-level rule).
    qualified_type: $ => prec.left(seq(
      $.identifier,
      '.',
      $.type_identifier,
      repeat(seq('.', $.type_identifier)),
    )),

    // `self` is a reserved keyword in type position — denotes the
    // implementing type inside interface signatures. Lowercase: matches the uniformly-lowercase keyword
    // space (`fn`, `pub`, `loop`, ...). Captured by a dedicated rule so
    // syntax highlighting can mark it as a keyword rather than a type
    // name.
    self_type: $ => 'self',

    type_parameter_identifier: $ => seq(
      $.type_identifier,
    ),

    _simple_type: $ => prec(1, seq(
      $.type_identifier,
      repeat(seq('.', $.type_identifier)),
    )),

    generic_type: $ => prec(2, seq(
      $.type_identifier,
      '<',
      commaSep1($._type),
      '>',
    )),

    function_type: $ => prec.right(3, seq(
      '(',
      commaSep($._type),
      ')',
      '->',
      $._type,
    )),

    tuple_type: $ => seq(
      '(',
      $._type,
      repeat1(seq(',', $._type)),
      ')',
    ),

    // Anonymous struct type — appears in any type-expression slot (function
    // parameter type, distinct-type tuple element, generic type argument,
    // struct field type). Top-level `type Foo {...}` is still rejected by the
    // Go parser at parseTypeDef; reaching this rule requires the surrounding
    // context to be a type slot. Field types are full type expressions; no
    // defaults (`=`) are allowed in type position.
    //
    // `prec(2)` biases the parse toward this rule when `{` appears in a
    // nested type-expression position — disambiguating from `body`-shaped
    // expressions (blocks, maps, anon-struct *literals*) that share the
    // brace syntax. Pairs with `prec(3)` on `field_definition` so struct
    // bodies and enum struct-payload variants prefer the with-default form.
    anon_struct_type: $ => prec(2, seq(
      '{',
      optional(seq(
        $.anon_struct_type_field,
        repeat(seq(',', $.anon_struct_type_field)),
        optional(','),
      )),
      '}',
    )),

    anon_struct_type_field: $ => seq(
      field('name', $._field_identifier),
      ':',
      field('type', $._type),
    ),

    // Body (block) — permissive to handle blocks, maps, anonymous structs.
    // Lambdas live in their own rule (|params| body) and no longer share `{}`.
    body: $ => seq('{', repeat(seq(choice($._item, $.map_entry, $.anon_struct_field), optional(','))), '}'),

    lambda: $ => prec.right(3, seq(
      '|',
      optional(commaSep1($.lambda_parameter)),
      '|',
      field('body', prec(2, $._lambda_body_expression)),
    )),

    _lambda_body_expression: $ => choice(
      $.identifier,
      $.type_identifier,
      $.dot_variant,
      $.field_accessor,
      $.integer,
      $.float,
      $.decimal,
      $.codepoint,
      $.string,
      $.triple_string,
      $.raw_string,
      $.tagged_type_string,
      $.tagged_type_triple_string,
      $.raw_tagged_string,
      $.boolean,
      $.list,
      $.vector,
      $.set,
      $.body,
      $.lambda,
      $.if_expression,
      $.case_expression,
      $.concurrent_block,
      $.struct_construction,
      $.map_construction,
      $.anon_struct,
      $.struct_update,
      $.map_literal,
      $.typed_list,
      $.call_expression,
      $.field_access,
      $.type_field_access,
      $.lambda_body_binary_expression,
      $.lambda_body_range_expression,
      $.lambda_body_unary_expression,
      $.try_expression,
      $.dbg_expression,
      $.todo_expression,
      $.paren_expression,
      $.tuple_expression,
      $.return_statement,
      $.break_statement,
      $.continue_statement,
    ),

    // A lambda parameter mirrors a function-definition parameter: a plain
    // name or an irrefutable destructuring pattern, with optional type
    // annotation and default. Reuses `_param_pattern` so distinct/struct/
    // tuple destructuring is available in lambdas exactly as in `fn`.
    lambda_parameter: $ => seq(
      field('name', $._param_pattern),
      optional(seq(':', $._type)),
      optional(seq('=', $._expression)),
    ),

    anon_struct_field: $ => prec(1, seq($._field_identifier, ':', $._expression)),

    // Headless anonymous-struct / map literals as values. They accept both the
    // scanner-classified brace tokens and a plain `{`; explicit conflicts with
    // `body` keep the ambiguous block/literal states alive until the contents
    // decide the parse. A head-hugging `Type{…}` brace stays construction.
    anon_struct: $ => prec(6, seq(choice('{', $._anon_struct_open), commaSep1($.anon_struct_field), optional(','), '}')),

    map_literal: $ => prec(6, seq(choice('{', $._map_open), commaSep1($.map_entry), optional(','), '}')),

    map_entry: $ => prec(2, seq($._expression, '=>', $._expression)),

    // Struct update by spread: `{..base, f: v}` — copy `base`, override the
    // named fields. The result takes the spread head's type, so no type name is
    // written, and `..` at the first position inside the brace is the whole
    // distinction from `anon_struct` (`{f: v}`). The external scanner classifies
    // such a brace as `_anon_struct_open` for exactly that reason.
    //
    // Position mirrors the list literal's spread rather than copying it. `list`
    // puts `..` LAST because a List is cons cells and prepending is the cheap
    // end; an update's order is pure notation, and base-then-overrides is the
    // only order under which "a later field wins" reads correctly. So: exactly
    // one spread, and it is the FIRST element. `{a: 1, ..b}` and `{..a, ..b}`
    // are not accepted here and land in the surrounding block/ERROR recovery,
    // matching the Go parser's `struct spread ".." must be the first element`.
    //
    // Separators are `optional(',')` rather than `commaSep`. `/\s/` is an extra,
    // so this one spelling covers the inline `{..base, x: 9}`, the trailing
    // comma the formatter emits on every multi-line comma list, and the stacked
    //   { \n ..base \n x: 9 \n }
    // form the Go parser also accepts (its field loop eats a COMMA or NEWLINE
    // run). `anon_struct` cannot do that — its stacked form degrades to a `body`
    // holding bare `anon_struct_field`s — so the update literal is the
    // better-resolved of the two, and a formatted file highlights either way.
    struct_update: $ => prec(6, seq(
      choice('{', $._anon_struct_open),
      $.struct_update_spread,
      repeat(seq(optional(','), $.struct_update_field)),
      optional(','),
      '}',
    )),

    // The spread element. `head` is a full expression, matching the Go parser,
    // which calls `parseExpr(1)` after the `..` exactly as the list spread does
    // — so `{..config, tag: "x"}`, `{..f(x), a: 1}` and `{..a.b, c: 2}` all
    // parse here and the checker, not the grammar, decides what is a struct.
    struct_update_spread: $ => seq('..', field('head', $._expression)),

    // A field in an update. Shaped like `struct_construction`'s field rather
    // than reusing `anon_struct_field`, because an update's field may be punned:
    // `{..base, x}` means `x: x`. `anon_struct_field` has no punned form — a
    // headless `{x}` classifies as a block — and widening it would make `{x}`
    // ambiguous between a block and a literal, so this stays a separate node.
    struct_update_field: $ => choice(
      seq(field('field_name', $._field_identifier), ':', $._expression),
      field('field_name', $._field_identifier),
    ),

    // Expression / statement catch-all for function bodies
    _expression_or_statement: $ => choice(
      $.binding,
      $.defer_statement,
      $.with_statement,
      $.pattern_assertion_binding,
      $.else_binding,
      $.assertion,
      $.return_statement,
      $.break_statement,
      $.continue_statement,
      $.destructuring_binding,
      $._expression,
    ),

    pattern_assertion_binding: $ => prec(0, seq(
      'assert',
      field('pattern', $._assertion_binding_pattern),
      '=',
      field('value', $._expression),
    )),

    // A binding whose pattern can fail, and what runs when it does:
    // `Some(e) = m else { return 0 }`, or case arms over the value that did
    // not match, `Ok(n) = r else { Err(e) -> return Err(e) }`. Braces always.
    //
    // The pattern is read in its expression spelling, as a case arm's escape
    // hatch reads one: `Some(e)` and `.Some(e)` are calls, `Ok((w, h))` a call
    // of a tuple, `[first, ..rest]` a list, `.Valid{addr, score}` a struct
    // construction. An expression statement is never followed by `=`, so the
    // `=` commits to this rule without making a statement-leading pattern
    // compete with the expression it looks like. A bare name is left out: it
    // always matches, and admitting it would let `x = if c { a } else { b }`
    // read its `else` as this rule's. A brace-led pattern (`{"k" => v}`,
    // `{a, b}`) and a tuple of names are a destructuring_binding that an
    // `else` follows.
    else_binding: $ => seq(
      choice(
        seq(
          field('pattern', $._else_binding_pattern),
          '=',
          field('value', $._expression),
        ),
        field('binding', $.destructuring_binding),
      ),
      'else',
      field('else', choice($.body, $.else_arms)),
    ),

    _else_binding_pattern: $ => choice(
      $.call_expression,
      $.list,
      $.tuple_expression,
      $.struct_construction,
    ),

    else_arms: $ => seq('{', repeat1(seq($.else_arm, optional($._case_arm_sep))), '}'),

    // An else arm is a case arm whose pattern is not a tuple, a literal or a
    // headless `{...}`: after `else {` a block and arms are both possible,
    // and the static precedence that makes a case arm's `(a, b)` a
    // tuple_pattern would make a block's `{ (80, 24) }` or
    // `{ {addr: "", score: 0} }` a pattern too. Those patterns still parse,
    // through the expression escape a case arm has.
    else_arm: $ => choice(
      prec.dynamic(20, seq(
        $._else_arm_pattern,
        optional(seq('when', $._expression)),
        '->',
        $._expression,
      )),
      seq(
        $._expression,
        optional(seq('when', $._expression)),
        '->',
        $._expression,
      ),
    ),

    _else_arm_pattern: $ => prec(2, choice(
      $.identifier,
      $.type_identifier,
      seq($.type_identifier, repeat1(seq('.', $.type_identifier))),
      $.dot_variant,
      '_',
      // Struct-variant patterns, `.Valid{addr}` and `Check.Valid{addr}`.
      seq($.dot_variant, '{', commaSep($.pattern_field), '}'),
      seq($.type_identifier, repeat(seq('.', $.type_identifier)), '{', commaSep($.pattern_field), '}'),
      $.enum_pattern,
      $.list_pattern,
    )),

    // Head-first assertion destructuring: `assert Ok(value) = parse()`.
    // Lowercase identifier starts are intentionally excluded so ordinary
    // assertions like `assert ready` cannot be confused with pattern asserts.
    _assertion_binding_pattern: $ => prec(2, choice(
      $.tuple_pattern,
      $.struct_pattern,
      $.enum_pattern,
      $.list_pattern,
      $.string,
      $.integer,
      $.float,
      $.decimal,
      $.codepoint,
      $.boolean,
      seq($.string, '+', $.identifier),
      seq('-', choice($.integer, $.float, $.decimal)),
    )),

    binding: $ => prec(-1, seq(
      $.identifier,
      optional(seq(':', $._type)),
      '=',
      choice($.assertion, $._expression),
    )),

    defer_statement: $ => seq(
      'defer',
      field('call', $._expression),
    ),

    assertion: $ => prec.right(1, seq(
      choice('assert', 'refute'),
      field('value', $._expression),
    )),

    bare_assertion: $ => choice('assert', 'refute'),

    destructuring_binding: $ => choice(
      // Tuple: (x, y) = expr
      seq(
        '(',
        commaSep1(field('binding', choice($.identifier, '_'))),
        ')',
        '=',
        field('value', $._expression),
      ),
      // Struct: {name, age} = expr  or  {name: n, age: a} = expr. The opening
      // brace may be scanner-classified `_anon_struct_open` (the `{name: n}`
      // rename form classifies as anon-struct) or plain `{` (`{name, age}`
      // punning classifies as a block).
      seq(
        choice('{', $._anon_struct_open),
        commaSep1(choice(
          field('binding', $.identifier),
          seq(field('field', $.identifier), ':', field('binding', choice($.identifier, '_'))),
        )),
        '}',
        '=',
        field('value', $._expression),
      ),
      // Map: {key => binding, ...} = expr — flat destructure binding the
      // value at each key. Keys are expressions (string/int/tuple/bool/...);
      // the `=>` distinguishes this from the struct form above (and classifies
      // the brace as `_map_open`).
      seq(
        choice('{', $._map_open),
        commaSep1(seq($._expression, '=>', field('binding', choice($.identifier, '_')))),
        '}',
        '=',
        field('value', $._expression),
      ),
    ),

    return_statement: $ => prec.right(seq('return', optional($._expression))),
    break_statement: $ => prec.right(seq('break', optional($._expression))),
    continue_statement: $ => 'continue',

    // Expressions
    _expression: $ => choice(
      $.identifier,
      $.type_identifier,
      $.dot_variant,
      $.field_accessor,
      $.integer,
      $.float,
      $.decimal,
      $.codepoint,
      $.string,
      $.triple_string,
      $.raw_string,
      $.tagged_type_string,
      $.tagged_type_triple_string,
      $.raw_tagged_string,
      $.boolean,
      $.list,
      $.vector,
      $.set,
      $.body,
      $.lambda,
      $.if_expression,
      $.case_expression,
      $.concurrent_block,
      $.struct_construction,
      $.map_construction,
      $.anon_struct,
      $.struct_update,
      $.map_literal,
      $.typed_list,
      $.call_expression,
      $.field_access,
      $.type_field_access,
      $.binary_expression,
      $.range_expression,
      $.unary_expression,
      $.pipe_expression,
      $.try_expression,
      $.dbg_expression,
      $.todo_expression,
      $.paren_expression,
      $.tuple_expression,
      $.return_statement,
      $.break_statement,
      $.continue_statement,
    ),

    // Expression excluding body — used where a { would be ambiguous (e.g., case value {)
    _non_block_expression: $ => choice(
      $.identifier,
      $.type_identifier,
      $.dot_variant,
      $.field_accessor,
      $.integer,
      $.float,
      $.decimal,
      $.codepoint,
      $.string,
      $.triple_string,
      $.raw_string,
      $.tagged_type_string,
      $.tagged_type_triple_string,
      $.raw_tagged_string,
      $.boolean,
      $.list,
      $.vector,
      $.set,
      $.if_expression,
      $.case_expression,
      $.concurrent_block,
      $.struct_construction,
      $.map_construction,
      $.anon_struct,
      $.struct_update,
      $.map_literal,
      $.typed_list,
      $.call_expression,
      $.lambda,
      $.field_access,
      $.type_field_access,
      $.binary_expression,
      $.range_expression,
      $.unary_expression,
      $.pipe_expression,
      $.try_expression,
      $.dbg_expression,
      $.todo_expression,
      $.paren_expression,
      $.tuple_expression,
    ),

    // Literals
    integer: $ => token(choice(
      /[0-9][0-9_]*/,
      /0x[0-9a-fA-F][0-9a-fA-F_]*/,
      /0b[01][01_]*/,
      /0o[0-7][0-7_]*/,
    )),

    float: $ => token(/[0-9][0-9_]*\.[0-9][0-9_]*([eE][+-]?[0-9][0-9_]*)?/),

    // Decimal literal: integer-or-fractional mantissa + a single d/D suffix
    // (e.g. 1.50d, 5d, 1_000.00d, 1.50D). No exponent notation — 1.5e3d is a
    // float 1.5e3 followed by ident d. Longest-match makes this win over
    // float/integer for the suffixed forms.
    decimal: $ => token(prec(2, /[0-9][0-9_]*(\.[0-9][0-9_]*)?[dD]/)),

    // ASCII codepoint literal: `'a'`, `'\n'`, `'\''`, `'\u{7F}'`. One
    // character or one escape between single quotes. The escapes are the
    // string escapes with `'` in place of `"`; they share the
    // `escape_sequence` node so highlighters treat both alike. The grammar
    // admits any one character; the Nomi lexer rejects one outside ASCII.
    codepoint: $ => seq(
      "'",
      choice(
        alias(token.immediate(/[^'\\\n]/), $.codepoint_content),
        alias(token.immediate(seq('\\', choice('n', 't', '\\', "'", /u\{[0-9a-fA-F]+\}/))), $.escape_sequence),
      ),
      token.immediate("'"),
    ),

    string: $ => seq(
      '"',
      repeat(choice(
        $.string_content,
        $.string_interpolation,
        $.escape_sequence,
      )),
      '"',
    ),

    // Triple-quoted string body: like `string`, `${...}` is an
    // interpolation slot. Backslash is literal — the Nomi lexer does no
    // escape processing in triple strings except `\${`, which the
    // external scanner keeps inside `triple_string_content`, so
    // `escape_sequence` is intentionally omitted from the body's choice set.
    triple_string: $ => seq(
      '"""',
      repeat(choice(
        $.triple_string_content,
        $.string_interpolation,
      )),
      '"""',
    ),

    // Raw backtick string. No `${...}` parsing, no escape processing —
    // backslash is literal, `\${` keeps its backslash. Body is one big
    // `raw_string_content` span emitted by the external scanner.
    raw_string: $ => seq(
      '`',
      optional($.raw_string_content),
      '`',
    ),

    // PascalCase tagged single-line: `<Tag>"..."`. The tag is a
    // type_identifier (its `impl TaggedLiteral` block backs the literal);
    // tagged literals are PascalCase-only. Split into its own rule
    // because tree-sitter's lexer chooses its lex state per
    // parser-position, and the no-skip state needed for the immediate
    // `"` lookahead doesn't activate after `type_identifier` when both
    // tags share one rule. `prec.right(1, ...)` resolves the
    // shift/reduce conflict — after the parser reads a
    // type_identifier, it could either reduce to a standalone
    // `_expression` (just the type) or shift toward
    // `tagged_type_string` expecting an immediate `"`. Without the
    // bias, tree-sitter picks reduce and `Date"..."` parses as
    // `(type_identifier)(string)`.
    tagged_type_string: $ => prec.right(1, seq(
      field('tag', $.type_identifier),
      alias(token.immediate('"'), '"'),
      repeat(choice(
        $.string_content,
        $.string_interpolation,
        $.escape_sequence,
      )),
      '"',
    )),

    // PascalCase tagged triple: `<Tag>"""..."""`. Same shift-bias as
    // the single-line variant above.
    tagged_type_triple_string: $ => prec.right(1, seq(
      field('tag', $.type_identifier),
      alias(token.immediate('"""'), '"""'),
      repeat(choice(
        $.triple_string_content,
        $.string_interpolation,
      )),
      '"""',
    )),

    // Raw-tagged backtick string: `<Tag>`...``. The tag is a PascalCase
    // type_identifier (its `impl TaggedLiteral` block backs the literal),
    // captured as a node so highlighters can pick it out.
    raw_tagged_string: $ => prec.right(1, seq(
      field('tag', $.type_identifier),
      alias(token.immediate('`'), '`'),
      optional($.raw_string_content),
      '`',
    )),

    // string_content / triple_string_content are produced by the
    // external scanner (see `src/scanner.c`). The scanner consumes
    // characters up to the next boundary (`"`, `\`, or `${` for
    // single-quoted strings; `"""` or `${` for triple-quoted strings) and
    // emits a single token. Doing this in C side-steps tree-sitter's
    // LR error recovery, which would otherwise treat a bare `{`
    // mid-string as an interpolation start and diverge from the lexer
    // in `lexer/lexer.go`. `#`, `#{`, `$` and `$$` are ordinary text.
    //
    // raw_string_content stops only at the closing backtick; it does
    // not treat `${` or `\` specially, matching the lexer's raw mode
    // (no interpolation, backslash is literal text).
    string_interpolation: $ => seq('${', $._expression, '}'),
    // `\${` writes a literal `${`; `\$` is valid only before `{`.
    escape_sequence: $ => token.immediate(seq('\\', choice('n', 't', '\\', '"', '${', /u\{[0-9a-fA-F]+\}/))),

    boolean: $ => choice('true', 'false'),

    list: $ => seq(
      '[',
      optional(seq(
        choice(seq('..', $._expression), $._expression),
        repeat(seq(',', choice(seq('..', $._expression), $._expression))),
        optional(','),
      )),
      ']',
    ),

    vector: $ => seq(
      '#[',
      optional(seq(
        $._expression,
        repeat(seq(',', $._expression)),
        optional(','),
      )),
      ']',
    ),

    set: $ => seq(
      '#{',
      optional(seq(
        $._expression,
        repeat(seq(',', $._expression)),
        optional(','),
      )),
      '}',
    ),

    paren_expression: $ => seq('(', $._expression, ')'),

    tuple_expression: $ => prec.dynamic(-1, prec(-1, seq('(', $._expression, repeat1(seq(',', $._expression)), optional(','), ')'))),

    // Control flow
    if_expression: $ => prec.right(seq(
      'if',
      choice($.if_match_condition, $._expression),
      $.body,
      optional(seq('else', choice($.if_expression, $.body))),
    )),

    if_match_condition: $ => seq(
      field('pattern', $._case_pattern),
      '=',
      field('value', $._expression),
    ),

    // Application field override, a statement: `with MyApp.logger = Silent{}`.
    // It lasts to the end of the enclosing block. The target is an
    // owner-qualified field read, spelled `Type.field` or `file.Type.field`,
    // and is a `field_access` node so the field-access highlighting applies
    // unchanged.
    with_statement: $ => prec.right(seq(
      'with',
      field('target', alias($._app_field_target, $.field_access)),
      '=',
      field('value', $._expression),
    )),

    _app_field_target: $ => seq(
      choice(
        $.type_identifier,
        alias($._qualified_app_type, $.type_field_access),
      ),
      token.immediate('.'),
      $._field_identifier,
    ),

    _qualified_app_type: $ => seq($.identifier, token.immediate('.'), $.type_identifier),

    pipe_if_expression: $ => prec.right(11, seq(
      'if',
      $.body,
      optional(seq('else', choice($.pipe_if_expression, $.body))),
    )),

    // Concurrent block: `concurrent { body }` — structured concurrency
    // scope. `Task.spawn(|| ...)` calls inside the block spawn tasks tied
    // to this scope; tasks must be awaited before the block exits.
    //
    // Body braces are spelled inline ('{' ... '}') rather than via
    // `$.body` so tree-sitter sees a distinct production it can pin
    // against the ambiguous "identifier + bare body" parse. `$.body`
    // is itself a valid bare expression in `_expression`, so when the
    // grammar used `field('body', $.body)`, the LR parser would
    // pre-empt the keyword form on the leading `concurrent` lexeme
    // and reduce it to a plain identifier expression followed by a
    // standalone body. (tree-sitter still factors the shared shape
    // back to the `body` rule node in the parse tree — a cosmetic
    // detail; the `concurrent_block` parent is what the Go pipeline
    // and the highlighter care about.)
    concurrent_block: $ => seq(
      'concurrent',
      '{',
      repeat(seq(choice($._item, $.map_entry, $.anon_struct_field), optional(','))),
      '}',
    ),

    case_expression: $ => prec(1, choice(
      // With scrutinee (`case x { pattern -> ... }`): arms match patterns.
      seq('case', $._non_block_expression, '{', repeat(seq($.case_branch, optional($._case_arm_sep))), '}'),
      // Scrutinee-less (`case { guard -> ... }`): arms are boolean guard
      // EXPRESSIONS, not patterns. Splitting the two forms (rather than sharing
      // one branch with `choice(_case_pattern, _expression)`) keeps a guard
      // like `Float.is_nan(a)` from being parsed as a malformed `Type.Variant`
      // enum_pattern — there is no pattern alternative here to compete with it.
      // `_case_arm_sep` (optional) lets the scanner end an arm before a
      // `.Uppercase` next arm so the body can't extend into it.
      seq('case', '{', repeat(seq($.guard_branch, optional($._case_arm_sep))), '}'),
    )),

    case_branch: $ => choice(
      prec.dynamic(20, seq(
        $._case_pattern,
        optional(seq('when', $._expression)),
        '->',
        $._expression,
      )),
      seq(
        $._expression,
        optional(seq('when', $._expression)),
        '->',
        $._expression,
      ),
    ),

    guard_branch: $ => seq(
      $._expression,
      '->',
      $._expression,
    ),

    _case_pattern: $ => prec(2, choice(
      $.identifier,
      $.type_identifier,
      // Qualified bare variant pattern: `Status.Open`, `Color.Red` (no
      // payload). Bare `Open` is the type_identifier alternative above; the
      // dotted form needs its own — `repeat1` requires a `.`, so it never
      // overlaps the bare one. (Payload forms are enum_pattern `Type.V(args)`
      // and struct_pattern `Type.V{fields}`.)
      seq($.type_identifier, repeat1(seq('.', $.type_identifier))),
      $.dot_variant,
      $.string,
      $.integer,
      $.float,
      $.decimal,
      $.codepoint,
      $.boolean,
      '_',
      $.tuple_pattern,
      $.struct_pattern,
      $.enum_pattern,
      $.list_pattern,
      // String prefix pattern: "hello" + rest
      seq($.string, '+', $.identifier),
      // Negative literal: -1, -3.14, -1.50d
      seq('-', choice($.integer, $.float, $.decimal)),
    )),

    struct_pattern: $ => prec(11, choice(
      // Named struct pattern: Type { field, field: pat }
      seq($.type_identifier, '{', commaSep($.pattern_field), '}'),
      // Qualified enum variant with a struct payload: `Mod.Variant{ field }`
      // (e.g. `IoError.NotFound{path}`). The `repeat1` requires at least one
      // `.`, so this matches only qualified heads — it never overlaps the bare
      // `Type { ... }` form above (which params also use, so leaving that one
      // untouched keeps parameter destructuring intact).
      seq($.type_identifier, repeat1(seq('.', $.type_identifier)), '{', commaSep($.pattern_field), '}'),
      // Anonymous struct pattern: { field, field: pat }. The opening brace may
      // arrive as the scanner-classified `_anon_struct_open` (a `{field: pat}`
      // pattern in a whitespace-preceded case-arm position classifies the same
      // as an anon-struct value) or as a plain `{` (`{field, field}` punning
      // classifies as a block; a no-space `({…}` isn't scanner-classified).
      seq(choice('{', $._anon_struct_open), commaSep($.pattern_field), '}'),
      // Anonymous map pattern: { key => pat, key => pat } — `_map_open` for the
      // scanner-classified (whitespace-preceded) case, plain `{` otherwise.
      seq(choice('{', $._map_open), commaSep(seq($._expression, '=>', $._case_pattern)), '}'),
      // Typed map pattern: TypeName { key => pat, key => pat } — flat
      // destructure of distinct map types (e.g., `Kvs{"a" => v}`).
      seq($.type_identifier, '{', commaSep1(seq($._expression, '=>', $._case_pattern)), '}'),
      // Dot-leading struct-variant pattern: .Variant{ field, field: pat }
      seq($.dot_variant, '{', commaSep($.pattern_field), '}'),
      // Dot-leading typed-map pattern: .Variant{ key => pat, key => pat }
      seq($.dot_variant, '{', commaSep1(seq($._expression, '=>', $._case_pattern)), '}'),
    )),

    pattern_field: $ => choice(
      seq(field('name', $._field_identifier), ':', $._case_pattern),
      field('name', $._field_identifier),
    ),

    tuple_pattern: $ => seq('(', $._case_pattern, repeat1(seq(',', $._case_pattern)), ')'),

    // The `prec(11)` is applied PER ALTERNATIVE rather than as an outer
    // `prec(11, choice(...))` wrapper. That distinction is load-bearing: the
    // bare/qualified head is a `_simple_type` (a dotted type name,
    // `type_identifier ('.' type_identifier)*`), and an outer `prec` wrapper
    // makes tree-sitter build a precedence-fragmented copy of the
    // `_simple_type` states that drops the `.` continuation in parameter
    // position (no `case`-arm `_expression` escape hatch there), so
    // `Enum.Variant(x)` parameters fail to parse. Per-alternative `prec` lets
    // the head reuse `_simple_type`'s native dotted-name states (the same ones
    // type annotations use), keeping `.` available.
    enum_pattern: $ => choice(
      // Bare `Type(args)` / `Variant(args)` and module-qualified
      // `Enum.Variant(args)` share one head — `_simple_type`, which already
      // models a dotted type name. Reusing it brings the qualified
      // `Enum.Variant(...)` form for free. In a `case` arm the qualified
      // spelling also matches the `_expression` escape hatch
      // (`type_field_access` + call); parameter position has no such fallback,
      // so the pattern grammar must accept the head directly.
      prec(11, seq($._simple_type, '(', commaSep($._case_pattern), ')')),
      // Dot-leading: .Variant(args)
      prec(11, seq($.dot_variant, '(', commaSep($._case_pattern), ')')),
    ),

    list_pattern: $ => choice(
      seq(
        '[',
        optional(seq(
          choice(seq('..', $._case_pattern), $._case_pattern),
          repeat(seq(',', choice(seq('..', $._case_pattern), $._case_pattern))),
          optional(','),
        )),
        ']',
      ),
      // Dot-leading list-variant pattern: .Variant[a, b, ..rest]
      seq(
        $.dot_variant,
        '[',
        optional(seq(
          choice(seq('..', $._case_pattern), $._case_pattern),
          repeat(seq(',', choice(seq('..', $._case_pattern), $._case_pattern))),
          optional(','),
        )),
        ']',
      ),
    ),

    // Operators
    binary_expression: $ => choice(
      ...[
        ['+', 8], ['-', 8], ['*', 9], ['/', 9], ['%', 9],
        ['==', 4], ['!=', 4], ['<', 5], ['>', 5], ['<=', 5], ['>=', 5],
      ].map(([op, p]) =>
        prec.left(p, seq($._expression, op, $._expression))
      ),
      prec.left(3, seq($._expression, 'and', $._expression)),
      prec.left(2, seq($._expression, 'or', $._expression)),
    ),

    lambda_body_binary_expression: $ => choice(
      ...[
        ['+', 8], ['-', 8], ['*', 9], ['/', 9], ['%', 9],
        ['==', 4], ['!=', 4], ['<', 5], ['>', 5], ['<=', 5], ['>=', 5],
      ].map(([op, p]) =>
        prec.left(p, seq($._lambda_body_expression, op, $._lambda_body_expression))
      ),
      prec.left(3, seq($._lambda_body_expression, 'and', $._lambda_body_expression)),
      prec.left(2, seq($._lambda_body_expression, 'or', $._lambda_body_expression)),
    ),

    // Range literal — bounded binary form only (`1..5`, `1..=5`).
    // Open-ended forms (`..5`, `1..`, `..`) were removed in favour of
    // `range.from(N)` / `range.naturals()` so that `..` can be the
    // unambiguous list-spread marker. Precedence sits between
    // pipe (6) and arithmetic (8) so `1+2..5+6` groups as
    // `(1+2)..(5+6)` and `1..5 == 1..5` as `(1..5) == (1..5)`.
    range_expression: $ => prec.left(7, seq($._expression, choice('..', '..='), $._expression)),
    lambda_body_range_expression: $ => prec.left(7, seq($._lambda_body_expression, choice('..', '..='), $._lambda_body_expression)),

    // Prefix unary operators: logical not (`!`) and arithmetic negation (`-`).
    // prec 10 binds tighter than binary `*`/`/` (9) and `+`/`-` (8), so
    // `-2 * 3` is `(-2) * 3` and `a - -b` is `a - (-b)`. Mirrors the Go
    // parser, which already represents `-x` as a unary expression.
    unary_expression: $ => prec(10, choice(seq('!', $._expression), seq('-', $._expression))),
    lambda_body_unary_expression: $ => prec(10, choice(seq('!', $._lambda_body_expression), seq('-', $._lambda_body_expression))),
    pipe_expression: $ => prec.left(6, seq($._expression, '|>', choice($.bare_assertion, $.assertion, $._expression, $.pipe_if_expression))),
    // Error propagation. Prefix `try expr` unwraps one postfix-level operand;
    // bare `try` is also valid as a pipe stage (`value |> try`), where the
    // previous pipe value supplies the operand.
    try_expression: $ => prec.right(10, seq('try', optional(field('value', $._expression)))),
    dbg_expression: $ => prec.right(1, seq('dbg', optional(field('value', $._expression)))),
    // A placeholder for code not yet written: `todo` or `todo "reason"`. The
    // reason is a string literal; the compiler rejects an interpolated one.
    todo_expression: $ => prec.right(1, seq('todo', optional(field('reason', choice($.string, $.triple_string, $.raw_string))))),

    struct_construction: $ => prec.dynamic(10, prec(10, seq(
      choice($.type_identifier, $.type_field_access, $.dot_variant),
      // The brace must hug the head with NO whitespace (`Point{x: 1}`), matching
      // the formatter. A space (`Type { ... }`) is therefore NOT a construction,
      // which is what lets `if cond { ... }` / `case x { ... }` keep the trailing
      // braced block as a body instead of grabbing it as `Type{...}`. (Go's
      // parser uses a `noStructLit` flag for the same disambiguation.)
      token.immediate('{'),
      commaSep(choice(
        seq(field('field_name', $._field_identifier), ':', $._expression),
        field('field_name', $._field_identifier),
      )),
      optional(','),
      '}',
    ))),

    // Map literal-attach: TypeName{key => value, ...} — used to construct
    // distinct map types (e.g., `pub type Kvs Map<String, Int>` followed by
    // `Kvs{"a" => 1}`). Parallel to struct_construction but with `=>` entries
    // instead of `:` fields. The bare anonymous map literal is parsed as a
    // `body` containing `map_entry` children; this rule covers the typed form.
    map_construction: $ => prec.dynamic(10, prec(10, seq(
      choice($.type_identifier, $.type_field_access, $.dot_variant),
      // No-whitespace brace — see struct_construction above.
      token.immediate('{'),
      commaSep1($.map_entry),
      optional(','),
      '}',
    ))),

    // Type-prefixed list literal: `Items[1, 2, 3]`, `JV.Arr[...]`, `.Arr[...]`
    // — attaches a list payload to a list-distinct type or a list-payload enum
    // variant. The list-literal sibling of map_construction (`Kvs{k => v}`).
    typed_list: $ => prec.dynamic(10, prec(10, seq(
      choice($.type_identifier, $.type_field_access, $.dot_variant),
      $.list,
    ))),

    _callable_expression: $ => choice(
      $.identifier,
      $.type_identifier,
      $.dot_variant,
      $.field_access,
      $.type_field_access,
      $.call_expression,
      $.paren_expression,
    ),

    // Dot-leading variant shorthand: `.Variant`. At expression position
    // it's a bare construction; with a payload (call, struct, map, list
    // literal) it's reached via the surrounding rule that admits a
    // dot_variant as its prefix. At pattern position the same rule is
    // matched via _case_pattern / struct_pattern / enum_pattern's
    // dot-leading alternatives.
    dot_variant: $ => prec(11, seq('.', $.type_identifier)),

    // Field accessor shorthand: `.name`, `.address.city`, `.0`. A function
    // that reads the field(s) from its argument; a lower-case name after the
    // leading dot is a field, an upper-case one a `dot_variant`.
    field_accessor: $ => prec.right(11, seq(
      '.',
      choice($._field_identifier, $.integer),
      repeat(seq(token.immediate('.'), choice($._field_identifier, $.integer))),
    )),

    call_expression: $ => prec.dynamic(5, prec(10, seq(
      field('function', $._callable_expression),
      // Optional turbofish type arguments: `f<Int>(x)`, `Channel.new<Int>(4)`.
      // The trailing `>(` distinguishes it from a comparison chain
      // (`f < Int > (x)`) — that has no `(` immediately after the `>`.
      optional(field('type_arguments', seq('<', commaSep1($._type), '>'))),
      '(',
      commaSep(choice(
        seq($.identifier, ':', $._expression),
        $._expression,
      )),
      ')',
    ))),

    // `self` is admitted as a field-access object (`self.method(x)`, the
    // in-block type-qualified call, design §4/§5) but NOT as a general
    // expression — a bare `self` only ever appears as the object of a
    // qualified call inside an impl-block body, so scoping it here (rather
    // than adding self_type to _expression) avoids the type-vs-expression
    // ambiguity `self` would otherwise create inside parens/tuples.
    field_access: $ => choice(
      prec(12, seq($.type_identifier, token.immediate('.'), choice($._field_identifier, $.integer))),
      prec(11, seq(choice($._expression, $.self_type), token.immediate('.'), choice($._field_identifier, $.integer))),
    ),
    type_field_access: $ => choice(
      prec(11, seq($.type_identifier, token.immediate('.'), $.type_identifier)),
      prec(11, seq($._expression, token.immediate('.'), $.type_identifier)),
    ),

    // Identifiers
    _field_identifier: $ => prec(1, choice($.identifier, alias($.host_keyword, $.identifier))),
    identifier: $ => /[a-z_][a-zA-Z0-9_]*\??/,
    type_identifier: $ => /[A-Z][a-zA-Z0-9_]*/,

    // Comments
    doc_comment: $ => token(prec(2, choice(
      '///',
      seq('///', /[^/\n].*/),
    ))),
    line_comment: $ => token(prec(1, choice(
      prec(3, /\/\/\/\/.*/),
      /\/\/[ \t]*/,
      seq('//', /[^>/\n].*/),
    ))),
  },
});

function commaSep(rule) {
  return optional(commaSep1(rule));
}

function commaSep1(rule) {
  // Optional trailing comma: the formatter emits one on every multi-line
  // comma list (params, call args, struct/list/map literals, type args, …),
  // and the interpreter's parser accepts it, so the grammar must too.
  return seq(rule, repeat(seq(',', rule)), optional(','));
}

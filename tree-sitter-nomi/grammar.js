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
    // The end of a statement or a `case` arm at a line break: zero-width, at
    // the start of the next line, and emitted only where a statement or an arm
    // can end. The Go lexer ends a statement at a line break after a token
    // that can end one unless the next line starts with `|>`, `)` or `]`, and
    // its parser steps over the break only before `else` and, after a
    // declaration's signature, before `{` and `where`. Without this token a
    // complete statement (`x = a`, `f`, `Foo`) would extend into the next line
    // as `a - 1`, the call `f (2)` or the typed list `Foo [1]`.
    $._statement_end,
    // Line-leading attached test prompts. The first prompt opens an attached
    // test node; following body-line prompts are named extras so prompted
    // attached tests keep normal Nomi parsing/highlighting after each `//!`.
    $.attached_comment_test_prompt_marker,
    $.attached_comment_test_line_prompt,
    $._attached_test_prompt_group_end,
    // Zero-width, before the operand of `dbg`, `try`, `return`, `break` and
    // `todo` when that operand starts on the keyword's line. The Go parser
    // ends the statement at a line break after the keyword, so `n |> dbg`
    // followed by a line `same = dbg n` is two statements, not `dbg same`.
    $._same_line,
    // The contextual keyword `subtests`: the word at the start of a
    // statement when an expression and a comma follow it, as the Go parser
    // reads it. Anywhere else it is an identifier.
    $._subtests_keyword,
    // The contextual keyword `group`: the word where a top-level item starts
    // when the group's name string follows it, as the Go parser reads it.
    // Anywhere else it is an identifier.
    $._group_keyword,
    // The contextual keyword `host`: the word that opens a host declaration
    // when `fn` or `type` follows it on its line, as the Go parser reads it.
    // Anywhere else it is an identifier, so a binding, parameter or field
    // named `host` reads as one in every position.
    $.host_keyword,
    // Zero-width, before a `{` that starts its line where a statement may
    // end before it. The brace opens a block statement, or after a
    // declaration's signature its body, as the Go parser steps over the line
    // break there. A block on the line of the statement before it is an
    // error, as two statements on one line are (`p = Point{}`).
    $._newline_brace,
  ],

  word: $ => $.identifier,

  conflicts: $ => [
    // `if #P{x, y} = p {`: a punned field is a struct literal's field or a
    // struct pattern's until the `=` after the `}` decides.
    [$.struct_construction, $.pattern_field],
    // `if #[.., z] = v` and `if #[.., z] == v`: a `..` is a vector
    // pattern's or a vector literal's bare spread, and a name after it the
    // pattern's rest or the spread's head, until the `=` or `==` decides.
    [$._collection_items, $.pattern_spread],
    [$._expression, $.pattern_spread],
    [$._expression, $._expression_or_statement],
    [$._expression, $._callable_expression],
    [$._expression, $._lambda_body_expression],
    [$._callable_expression, $._lambda_body_expression],
    [$._lambda_body_expression, $._expression, $._callable_expression],
    [$._expression, $.destructuring_binding],
    [$.destructuring_binding, $.struct_pattern],
    [$._expression, $.enum_pattern, $._callable_expression],
    [$.enum_pattern, $._callable_expression],
    // `self(...)` is the pattern `self(p)` or the construction `self(x)`
    // wherever a pattern may be read as an expression; what follows the
    // `)` decides, as for `Type(...)` above.
    [$.self_type, $.self_expression],
    [$.list, $.list_pattern],
    [$._expression, $.pattern_field],
    [$.destructuring_binding, $.pattern_field],
    [$.destructuring_binding, $._expression, $.pattern_field],
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
    // `Ok(n) = r` is a pattern_binding unless an `else` follows its value.
    [$.pattern_binding, $._else_binding_pattern],
    // `#{y, x: _c}`: an anonymous struct (a struct pattern with `as`) and a
    // destructure stay alive until the `=` decides.
    [$.anon_struct, $.destructuring_binding],
    // `#(#(a, b) as inner, c) = pair`: a destructure's nested pattern reads as
    // the expression it is spelled as until the `=` decides.
    [$._destructuring_subpattern, $._expression],
    // `fn` starts a declaration or a lambda. A declaration's name is
    // followed by its `(` or `<`, a lambda's parameter by `,`, `:`, `=` or
    // `->`; until then a capitalized or dotted name reads both ways
    // (`fn Point.show(` is an impl-qualified declaration, `fn Point(n) ->`
    // a lambda taking a Point apart).
    [$.function_definition, $._simple_type],
  ],

  rules: {
    // A `#!` interpreter line, allowed only as the file's first line, so a
    // script runs as `./hi.nomi`. The compiler's lexer skips it.
    source_file: $ => seq(optional($.shebang), statements($)),

    shebang: _ => token(seq('#!', /.*/)),

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
      $.group_declaration,
      $.import_statement,
      $.import_block,
      $.gopkg_declaration,
      $._expression_or_statement,
    ),

    // Visibility marker: `pub` makes a top-level declaration part of
    // the module's public surface. Wrapped as an explicit `pub` rule so
    // tree-sitter treats it as an anonymous keyword token (matching
    // `extern`, `once`, `fn`, etc.) rather than a free-standing
    // identifier reachable through `_expression_or_statement`.
    pub: $ => 'pub',

    // `export fn`: a top-level function a host calls by name. A rule of its
    // own for the same reason as `pub`.
    export: $ => 'export',

    attached_test_prompt: $ => seq(
      $.attached_comment_test_prompt_marker,
      // A prompt body is a block: it may declare local types and functions
      // (`//! type TraceId String`).
      field('body', repeat1(seq(
        choice(
          $.import_statement,
          $.import_block,
          $.function_definition,
          $.type_definition,
          $.struct_definition,
          $.enum_definition,
          $.type_alias,
          $.interface_definition,
          $.impl_block,
          $.derive_conformance,
          $._expression_or_statement,
        ),
        optional($._statement_end),
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
    // A leading `pub` re-exports every item the import selects from this
    // file, under its local name:
    //   pub import some_mod.{a, b}
    //   pub import some_mod.{Helper as Helpr}
    import_statement: $ => seq(optional($.pub), 'import', $._import_body),

    // Import block: `import { entry entry ... }` — bundles multiple imports
    // into a single statement. Entries use the same `_import_body` shape as a
    // standalone import. Commas appear only inside an entry's selective name
    // list (`mod.{a, b}`). `pub import { ... }` re-exports every entry.
    import_block: $ => seq(
      optional($.pub),
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
    //   path.Owner.Member         (a member through its owner: `std/iter.Iter.loop`)
    //   path.{Names}, path.Owner.{Names}
    //   path: Type                (selector-list selective)
    //   path: Owner.Type          (single-name owner selective)
    //   path: Owner.{Names}       (owner selective)
    //   path: {Names}             (flat selective)
    // plus an optional `as` alias. The `.` boundary is the
    // owner/member boundary inside the declaration selector; `/` joins
    // module-path segments inside `module_path`.
    _import_body: $ => seq(
      $.module_path,
      optional(choice(
        $.import_selector,
        seq(token.immediate('.'), choice(
          $.import_names,
          seq(
            $._import_member,
            repeat(seq(token.immediate('.'), $._import_member)),
            optional(seq(token.immediate('.'), $.import_names)),
          ),
        )),
      )),
      optional(seq('as', choice($.identifier, $.type_identifier))),
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

    _import_path_keyword: _ => token(prec(-1, /and|as|assert|break|case|continue|dbg|defer|else|enum|export|extern|fn|for|if|impl|import|interface|once|opaque|or|pub|refute|return|self|setup|struct|tap|test|then|todo|try|type(alias)?|where|with/)),

    import_selector: $ => prec.right(seq(
      ':',
      commaSep1($.import_selector_clause),
    )),

    import_selector_clause: $ => choice(
      $.import_names,
      prec.right(1, seq(
        choice($.identifier, $.type_identifier),
        repeat(seq(token.immediate('.'), choice($.identifier, $.type_identifier))),
        token.immediate('.'),
        $.import_names,
      )),
      prec(-1, $.import_selector_path),
    ),

    import_selector_path: $ => prec.right(seq(
      choice($.identifier, $.type_identifier),
      repeat(seq(token.immediate('.'), choice($.identifier, $.type_identifier))),
    )),

    import_value_selector: $ => $.identifier,

    // One `.`-joined member after the path: an owner type, a variant or a
    // value, as in `std/iter.Iter.loop` or `results.Result.Ok`.
    _import_member: $ => choice($.type_identifier, $.import_value_selector),

    import_names: $ => seq('{', commaSep1(choice($.import_self, $.import_entry)), '}'),

    // `self` inside a selective brace list lifts the LHS of the brace
    // group: `import path.{self, X, Y}` also binds `path` (the module);
    // `import path.Enum.{self, X, Y}` also binds `Enum` (the type).
    // No alias — it's a marker, not a name.
    import_self: $ => 'self',

    // One name in a selective-import brace list:
    //   Name
    //   Name as Local
    import_entry: $ => seq(
      choice($.identifier, $.type_identifier),
      repeat(seq('.', choice($.identifier, $.type_identifier))),
      optional(seq('as', choice($.identifier, $.type_identifier))),
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
      token.immediate('.'),
      field('symbol', choice($.identifier, $.type_identifier)),
    ),

    // Functions
    function_definition: $ => prec.right(seq(
      optional(choice($.pub, $.export)),
      optional('impl'),
      'fn',
      choice(
        seq(
          field('impl_interface', choice(
            $.type_identifier,
            seq(choice($.identifier, $.type_identifier), token.immediate('.'), $.type_identifier),
          )),
          token.immediate('.'),
          field('name', choice($.identifier, $.type_identifier)),
        ),
        field('name', choice(
          $.identifier,
          $.type_identifier,
        )),
      ),
      optional($.type_parameters),
      $.parameters,
      optional(seq('->', field('return_type', $._type))),
      optional($.where_clause),
      optional(choice($.go_selector_binding, seq(optional($._newline_brace), $.body))),
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
            seq(choice($.identifier, $.type_identifier), token.immediate('.'), $.type_identifier),
          )),
          token.immediate('.'),
          field('name', choice($.identifier, $.type_identifier)),
        ),
        field('name', choice($.identifier, $.type_identifier)),
      ),
      optional($.type_parameters),
      $.parameters,
      optional(seq('->', field('return_type', $._type))),
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
      $.map_pattern,
      $.vector_pattern,
      $.enum_pattern,
      alias($._param_or_pattern, $.or_pattern),
      alias($._param_as_pattern, $.as_pattern),
    ),

    // `.L(n) or .R(n)` in parameter position, whose alternatives together
    // match every argument.
    _param_or_pattern: $ => prec.left(2, seq(
      field('alternative', $._param_pattern),
      'or',
      field('alternative', $._param_pattern),
    )),

    // `#(a, b) as pair` in parameter position: the parameter's pattern and a
    // name for the whole argument. The name is as_pattern's.
    _param_as_pattern: $ => prec.left(1, seq(
      field('pattern', $._param_pattern),
      'as',
      field('name', choice($.identifier, $.type_identifier)),
    )),

    type_parameters: $ => seq('<', commaSep1($.type_parameter), '>'),
    // Generic headers introduce type-parameter names only. Bounds live in a
    // following `where` clause (`fn f<T>(x: T) -> T where T: Display`).
    type_parameter: $ => seq(
      field('name', $.type_parameter_identifier),
    ),

    qualified_type_identifier: $ => prec.left(seq(
      $.type_identifier,
      repeat(seq(token.immediate('.'), $.type_identifier)),
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
    //   pub type Foo #(T, U)            → tuple-distinct
    //   pub type Foo                    → zero-sized
    // Struct and enum forms are separate top-level rules
    // (struct_definition, enum_definition).
    //
    // Modifier order: `pub`? `opaque`? `type`. `opaque` is only valid on
    // distinct types (those with an `Inner` representation); the analyzer
    // enforces.
    //
    // The inner type is `_distinct_inner_type` (not the full `_type`):
    // the Go parser only admits TYPE_IDENT / IDENT / LPAREN / `#(` starts there,
    // which keeps a brace after the name unambiguous — `type Foo {` is
    // always the item body (`struct` owns record types).
    type_definition: $ => prec.right(seq(
      optional($.pub),
      optional('opaque'),
      'type',
      field('name', $.qualified_type_identifier),
      optional($.type_parameters),
      optional(choice(
        $.go_selector_binding,
        seq($._distinct_inner_type, optional($.type_body)),
        $.type_body,
      )),
    )),

    // Type and extern-type bodies hold only the type's own qualified API.
    // Fields belong to structs; variants belong to enums; interface impls and
    // derives stay outside the type.
    type_body: $ => seq(
      optional($._newline_brace),
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
    // except `self` and an anonymous struct type (`struct` owns record
    // types; anon structs remain reachable in nested positions like tuple
    // elements and generic args).
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
    struct_body: $ => typeBody($, choice(
      $.field_definition,
      $.function_definition,
      $.extern_func_definition,
      $.once_binding,
    ), $._decorated_inherent_item),

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

    enum_body: $ => typeBody($, choice(
      $.enum_variant,
      $.function_definition,
      $.extern_func_definition,
      $.once_binding,
    ), $._decorated_inherent_item),

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
    // Interface bodies hold functions only: `interface_method`, a plain
    // `fn ...` declaration (signature-only = required; with a body =
    // default method), optionally prefixed with the contextual `open`
    // keyword to mark a default as overridable (without `open`, defaults
    // are final), and optionally `extern` for host-backed defaults.
    // `open` is a CONTEXTUAL keyword in Nomi — it only carries meaning
    // inside an interface body. Because the literal string appears *only*
    // inside this rule, tree-sitter's keyword extraction won't elevate it
    // to a global reserved word; outside an interface body it continues to
    // lex as a plain identifier, so user code with a local named `open`
    // keeps working.
    interface_definition: $ => seq(
      optional($.pub),
      'interface',
      field('name', $.type_identifier),
      optional($.type_parameters),
      optional(seq('extends', $._type)),
      optional($.where_clause),
      typeBody($, $.interface_method),
    ),

    interface_method: $ => seq(
      optional('open'),
      optional(choice('extern', $.host_keyword)),
      'fn',
      field('name', choice($.identifier, $.type_identifier)),
      optional($.type_parameters),
      $.parameters,
      optional(seq('->', field('return_type', $._type))),
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
    // `self` as the name bounds the implementing type for one interface
    // default (`where self: Display`).
    where_constraint: $ => seq(
      field('name', choice($.type_parameter_identifier, $.self_type)),
      ':',
      $._type,
      repeat(seq('and', $._type)),
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
      optional($._newline_brace),
      '{',
      repeat(choice($.decorated_impl_item, $.function_definition, $.extern_func_definition, $.once_binding)),
      '}',
    ),

    // `test "name" { }`, or `test "name", pattern { }` binding the group's
    // setup value with any irrefutable pattern (`db`, `#(a, b)`, `#{a, b}`).
    test_declaration: $ => seq(
      'test',
      field('name', $.string),
      optional(seq(',', field('pattern', $._param_pattern))),
      $.body,
    ),

    // The compiler rejects a group without a name; the grammar accepts it,
    // so an editor keeps highlighting `group` as a keyword and the body as a
    // group's.
    group_declaration: $ => seq(
      alias($._group_keyword, 'group'),
      optional(field('name', $.string)),
      $.group_body,
    ),

    // A group holds at most one each of its `clock`, `boot` and `setup` lines
    // and its tests, in any order: the lines run in a fixed order whatever
    // their position. The compiler rejects a second `clock`, `boot` or
    // `setup`; the grammar accepts it, so an editor keeps highlighting.
    group_body: $ => seq(
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
    //   derive ToJson for User with #ToJson.Options{rename_all: Json.Case.Camel}
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

    // Enum positional payloads (`Some T`) stay on the variant name's line:
    // the enum body ends a variant at a line break (`_statement_end`), so
    // adjacent bare variants (`A` newline `B`) are two variants.
    _enum_payload_type: $ => choice(
      $._enum_payload_simple_type,
      $._enum_payload_generic_type,
      $._enum_payload_function_type,
      $._enum_payload_tuple_type,
      $._enum_payload_parenthesized_type,
    ),

    _enum_payload_simple_type: $ => prec(1, seq(
      $.type_identifier,
      repeat(seq(token.immediate('.'), $.type_identifier)),
    )),

    _enum_payload_generic_type: $ => prec(2, seq(
      $.type_identifier,
      '<',
      commaSep1($._type),
      '>',
    )),

    _enum_payload_function_type: $ => prec.right(3, seq(
      '(',
      commaSep($._type),
      ')',
      '->',
      $._type,
    )),

    _enum_payload_tuple_type: $ => seq(
      '#(',
      $._type,
      repeat1(seq(',', $._type)),
      optional(','),
      ')',
    ),

    // `Num (Int)` / `Num ((Int) -> Int)`: one payload type in parentheses,
    // which the Go parser reads as the type itself (there are no 1-tuples).
    _enum_payload_parenthesized_type: $ => seq(
      '(',
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
      token.immediate('.'),
      $.type_identifier,
      repeat(seq(token.immediate('.'), $.type_identifier)),
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
      repeat(seq(token.immediate('.'), $.type_identifier)),
    )),

    // `Map<K, V>`, or a module-qualified one: `tasks.Task<Int>`.
    generic_type: $ => prec(2, seq(
      choice($.type_identifier, $.qualified_type),
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
      '#(',
      $._type,
      repeat1(seq(',', $._type)),
      optional(','),
      ')',
    ),

    // Anonymous struct type, `#{x: Int, y: Int}`, as its value is written —
    // appears in any type-expression slot (function parameter type,
    // distinct-type tuple element, generic type argument, struct field type).
    // Field types are full type expressions; no defaults (`=`) are allowed in
    // type position.
    anon_struct_type: $ => prec(2, seq(
      '#{',
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

    // Body (block). Lambdas live in their own rule (`fn params -> body`). A
    // `{` always opens a block: a struct literal with no type name opens with
    // `#{`.
    body: $ => seq('{', statements($), '}'),

    // A lambda, `fn params -> body`. Its body runs to the end of its
    // expression, a `|>` included: `fn s -> f(s) |> g()` pipes inside the
    // body. A parameter's annotation is a full type, so `fn f: (Int) -> Int
    // -> f(1)` reads the function type `(Int) -> Int` whole before the
    // lambda's own `->`.
    lambda: $ => prec.right(3, seq(
      'fn',
      optional(commaSep1($.lambda_parameter)),
      '->',
      field('body', prec(2, $._expression)),
    )),

    // The pipe stage `x |> then fn v -> body` applies the lambda to the
    // piped value. Its lambda's body ends at the next `|>` of the pipeline,
    // so it is parsed from `_lambda_body_expression`, which has no pipe;
    // braces keep a pipe inside it. `x |> then &f(a, &1)` applies a
    // capture, which ends with its body.
    then_stage: $ => seq('then', choice(
      field('lambda', alias($._then_lambda, $.lambda)),
      field('capture', $.capture),
    )),

    // The pipe stage `x |> tap fn v -> body` runs the lambda (or capture) on
    // the piped value and passes the value on. Its lambda is a `then`
    // lambda: the body ends at the next `|>`.
    tap_stage: $ => seq('tap', choice(
      field('lambda', alias($._then_lambda, $.lambda)),
      field('capture', $.capture),
    )),

    // A call body is preferred over ending the lambda early: without it,
    // `then fn v -> io.print(v)` parsed as `(x |> then fn v -> io).print(v)`.
    _then_lambda: $ => prec.right(3, seq(
      'fn',
      optional(commaSep1($.lambda_parameter)),
      '->',
      field('body', prec(2, choice(prec.dynamic(1, $.call_expression), $._lambda_body_expression))),
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
      $.body,
      $.lambda,
      $.if_expression,
      $.case_expression,
      $.concurrent_block,
      $.struct_construction,
      $.map_construction,
      $.anon_struct,
      $.struct_update,
      $.vector,
      $.set,
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

    // A struct literal with no type name, `#{x: 1, y: 2}`: the struct its
    // position expects, or an anonymous struct. A punned field (`#{x, y}`)
    // is a bare identifier child, as a struct update's punned field is: it
    // names a variable.
    anon_struct: $ => prec(6, seq(
      '#{',
      commaSep(choice($.anon_struct_field, $.identifier)),
      '}',
    )),

    map_entry: $ => prec(2, seq($._expression, '=>', $._expression)),

    // Struct update by spread: `#{..base, f: v}` — copy `base`, override the
    // named fields. The result takes the spread head's type, so no type name is
    // written, and `..` at the first position inside the brace is the whole
    // distinction from `anon_struct` (`#{f: v}`).
    //
    // Position mirrors the list literal's spread rather than copying it. `list`
    // puts `..` LAST because a List is cons cells and prepending is the cheap
    // end; an update's order is pure notation, and base-then-overrides is the
    // only order under which "a later field wins" reads correctly. So: exactly
    // one spread, and it is the FIRST element. `#{a: 1, ..b}` and `#{..a, ..b}`
    // are not accepted here and land in the surrounding block/ERROR recovery,
    // matching the Go parser's `struct spread ".." must be the first element`.
    //
    // Separators are `optional(',')` rather than `commaSep`. `/\s/` is an extra,
    // so this one spelling covers the inline `#{..base, x: 9}`, the trailing
    // comma the formatter emits on every multi-line comma list, and the stacked
    //   #{ \n ..base \n x: 9 \n }
    // form the Go parser also accepts (its field loop eats a COMMA or NEWLINE
    // run).
    struct_update: $ => prec(6, seq(
      '#{',
      $.struct_update_spread,
      repeat(seq(optional(','), $.struct_update_field)),
      optional(','),
      '}',
    )),

    // The spread element. `head` is a full expression, matching the Go parser,
    // which calls `parseExpr(1)` after the `..` exactly as the list spread does
    // — so `#{..config, tag: "x"}`, `#{..f(x), a: 1}` and `#{..a.b, c: 2}` all
    // parse here and the checker, not the grammar, decides what is a struct.
    // A `..` with no head (`#{.., city: "NYC"}`) is the bare spread: the value
    // already at the field or key the literal is written at. The token after
    // it, a `,` or the closing `}`, decides it.
    struct_update_spread: $ => prec.right(seq('..', optional(field('head', $._expression)))),

    map_update_spread: $ => prec.right(seq('..', optional(field('head', $._expression)))),

    // A field in an update, shaped like `struct_construction`'s field: it may
    // be punned, `#{..base, x}` meaning `x: x`.
    struct_update_field: $ => choice(
      seq(field('field_name', $._field_identifier), ':', $._expression),
      field('field_name', $._field_identifier),
    ),

    // Expression / statement catch-all for function bodies
    _expression_or_statement: $ => choice(
      $.binding,
      $.as_binding,
      $.defer_statement,
      $.with_statement,
      $.pattern_assertion_binding,
      $.else_binding,
      $.pattern_binding,
      $.assertion,
      $.subtests_statement,
      $.return_statement,
      $.break_statement,
      $.continue_statement,
      $.destructuring_binding,
      $._expression,
    ),

    // `assert Ok(value) = parse()`: an assertion that a pattern matches,
    // binding its names. The pattern is read in its expression spelling, as
    // else_binding's is (`_` lexes as an identifier there), so it never
    // competes with the expression an ordinary assertion starts with:
    // `assert (1, 2) == (1, 2)` and `assert Some(1) == Some(1)` stay
    // comparisons, and the `=` alone decides.
    pattern_assertion_binding: $ => seq(
      'assert',
      field('pattern', $._expression),
      '=',
      field('value', $._expression),
    ),

    // A binding whose pattern can fail, and what runs when it does:
    // `Some(e) = m else { return 0 }`, or case arms over the value that did
    // not match, `Ok(n) = r else { Err(e) -> return Err(e) }`. Braces always.
    //
    // The pattern is read in its expression spelling, as a case arm's escape
    // hatch reads one: `Some(e)` and `.Some(e)` are calls, `Ok(#(w, h))` a call
    // of a tuple, `[first, ..rest]` a list, `#[a, ..]` a vector,
    // `#.Valid{addr, score}` a struct construction. An expression statement is never followed by `=`, so the
    // `=` commits to this rule without making a statement-leading pattern
    // compete with the expression it looks like. A bare name is left out: it
    // always matches, and admitting it would let `x = if c { a } else { b }`
    // read its `else` as this rule's. A map pattern (`%{"k" => v}`), a
    // struct pattern with no type name (`#{a, b}`) and a tuple of names are a
    // destructuring_binding that an `else` follows.
    //
    // Its dynamic precedence outranks pattern_binding's: where `else` is not
    // valid it lexes as an identifier, so `Some(e) = m` followed by the
    // statements `else` and `{ ... }` is a reading that has no error too.
    else_binding: $ => prec.dynamic(2, seq(
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
    )),

    // A binding whose variant or struct pattern always matches, so it has no
    // `else`: `Day(n) = d`, `#Point{x, y} = p`. The pattern is read in its
    // expression spelling, as else_binding's is. Without this rule `Day(n) =
    // d` read as two statements, `Day` and the destructure `(n) = d`; the
    // dynamic precedence keeps that reading from winning.
    pattern_binding: $ => prec.dynamic(1, seq(
      // `.L(n) or .R(n) = t`: alternatives that together always match,
      // spelled as the boolean `or` of their expression spellings.
      field('pattern', choice($.call_expression, $.struct_construction, $.binary_expression)),
      '=',
      field('value', $._expression),
    )),

    // A binding whose pattern ends in `as name`: `#(x, y) as both = pair`,
    // `Some(n) as m = find() else { return 0 }`. The pattern is read in its
    // expression spelling, as else_binding's is, so a statement-leading
    // pattern never competes with the expression it looks like; an
    // expression statement is never followed by `as`, so the `as` commits.
    // A struct pattern with no type name is an anon_struct (`#{x: _c, y} as p`).
    as_binding: $ => prec.right(seq(
      field('pattern', alias($._expression_as_pattern, $.as_pattern)),
      '=',
      field('value', $._expression),
      optional(seq('else', field('else', choice($.body, $.else_arms)))),
    )),

    _else_binding_pattern: $ => choice(
      $.call_expression,
      $.list,
      $.vector,
      $.set,
      $.tuple_expression,
      $.struct_construction,
      // `.A(n) or .B(n) = e else { ... }`: alternatives in their
      // expression spelling, which is the boolean `or`.
      $.binary_expression,
    ),

    else_arms: $ => seq('{', repeat1(seq($.else_arm, optional($._statement_end))), '}'),

    // An else arm is a case arm whose pattern is not a tuple, a literal or a
    // headless `{...}`: after `else {` a block and arms are both possible,
    // and the static precedence that makes a case arm's `#(a, b)` a
    // tuple_pattern would make a block's `{ #(80, 24) }` or
    // `{ #{addr: "", score: 0} }` a pattern too. Those patterns still parse,
    // through the expression escape a case arm has.
    else_arm: $ => choice(
      prec.dynamic(20, seq(
        $._else_arm_pattern,
        optional(seq('when', $._expression)),
        '->',
        $._arm_body,
      )),
      seq(
        $._expression,
        optional(seq('when', $._expression)),
        '->',
        $._arm_body,
      ),
    ),

    _else_arm_pattern: $ => prec(2, choice(
      $.identifier,
      $.type_identifier,
      seq($.type_identifier, repeat1(seq(token.immediate('.'), $.type_identifier))),
      $.dot_variant,
      '_',
      // Patterns that name their type, `#.Valid{addr}`, `#Check.Valid{addr}`
      // and `%Kvs{"k" => v}`.
      alias($._named_struct_pattern, $.struct_pattern),
      alias($._named_map_pattern, $.map_pattern),
      $.enum_pattern,
      $.list_pattern,
      $.vector_pattern,
      $.set_pattern,
      alias($._else_arm_or_pattern, $.or_pattern),
      alias($._else_arm_as_pattern, $.as_pattern),
    )),

    // `Err(.Empty) or Err(.Blank) ->`, an else arm's alternatives.
    _else_arm_or_pattern: $ => prec.left(2, seq(
      field('alternative', $._else_arm_pattern),
      'or',
      field('alternative', $._else_arm_pattern),
    )),

    // `Err(.NotFound) as e ->`. Its pattern is an else arm's, not any case
    // pattern, for the reason else_arm gives.
    _else_arm_as_pattern: $ => prec.left(1, seq(
      field('pattern', $._else_arm_pattern),
      'as',
      field('name', choice($.identifier, $.type_identifier)),
    )),

    binding: $ => prec(-1, choice(
      seq($.identifier, '=', choice($.assertion, $._expression)),
      seq($._field_identifier, ':', $._type, '=', choice($.assertion, $._expression)),
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

    // `subtests rows, pattern { body }`: the body runs once per row, with the
    // pattern bound to it. `subtests rows, pattern, name { body }` names each
    // row with a String expression that reads the pattern's bindings.
    subtests_statement: $ => seq(
      alias($._subtests_keyword, 'subtests'),
      field('rows', $._expression),
      ',',
      field('pattern', $._param_pattern),
      optional(seq(',', field('name', $._expression))),
      field('body', $.body),
    ),

    destructuring_binding: $ => choice(
      // Tuple: #(x, y) = expr, or #Pair(x, y) = expr for a distinct type
      // over a tuple.
      seq(
        choice('#(', seq('#', field('type', $._literal_head), '(')),
        commaSep1(choice(
          field('binding', choice($.identifier, '_')),
          field('pattern', $._destructuring_subpattern),
        )),
        ')',
        '=',
        field('value', $._expression),
      ),
      // Struct: #{name, age} = expr  or  #{name: n, age: a} = expr.
      seq(
        '#{',
        commaSep1(choice(
          field('binding', $.identifier),
          // `_field_identifier`, as anon_struct_field's name is, so the two
          // readings of `#{x: a` stay alive until `=` decides.
          seq(field('field', $._field_identifier), ':', choice(
            field('binding', choice($.identifier, '_')),
            field('pattern', $._destructuring_subpattern),
          )),
        )),
        '}',
        '=',
        field('value', $._expression),
      ),
      // Map: %{key => binding, ...} = expr, or %Kvs{...} = expr — flat
      // destructure binding the value at each key. Keys are expressions
      // (string/int/tuple/bool/...).
      seq(
        choice('%{', seq('%', field('type', $._literal_head), '{')),
        commaSep1(seq($._expression, '=>', choice(
          field('binding', choice($.identifier, '_')),
          field('pattern', $._destructuring_subpattern),
        ))),
        '}',
        '=',
        field('value', $._expression),
      ),
    ),

    // A destructure's nested pattern other than a name, in its expression
    // spelling: `#(#(a, b) as inner, c) = pair`, `#{x: Some(a), y} = q else
    // { ... }`, `%{"k" => #(a, b)} = m`.
    _destructuring_subpattern: $ => choice(
      alias($._expression_as_pattern, $.as_pattern),
      $.call_expression,
      $.tuple_expression,
      $.list,
      $.vector,
      $.set,
      $.struct_construction,
      $.map_construction,
      $.anon_struct,
      // A literal or a bare variant a field must equal, `#{x: 0, y} = p else
      // { ... }` or `#{done: True, n} = s else { ... }`, which only a binding
      // with `else` can use.
      $.integer,
      $.float,
      $.decimal,
      $.codepoint,
      $.string,
      $.boolean,
      $.type_identifier,
      $.type_field_access,
      $.dot_variant,
    ),

    return_statement: $ => prec.right(seq('return', optional(seq($._same_line, $._expression)))),
    break_statement: $ => prec.right(seq('break', optional(seq($._same_line, $._expression)))),
    continue_statement: $ => 'continue',

    // Expressions
    _expression: $ => choice(
      $.identifier,
      $.placeholder,
      $.capture,
      $.self_expression,
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
      $.body,
      $.lambda,
      $.if_expression,
      $.case_expression,
      $.concurrent_block,
      $.struct_construction,
      $.map_construction,
      $.anon_struct,
      $.struct_update,
      $.vector,
      $.set,
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
      alias($._expression_as_pattern, $.as_pattern),
    ),

    // `P as name` where a pattern is read in its expression spelling: the
    // left side of a binding, a pattern assertion, an arm that took the
    // expression reading, and a pattern nested in one
    // (`Some(#(a, b) as p) as m = opt`). No expression is followed by `as`,
    // so only a pattern reads this way.
    _expression_as_pattern: $ => prec.left(1, seq(
      field('pattern', $._expression),
      'as',
      field('name', choice($.identifier, $.type_identifier)),
    )),

    // Expression excluding body — used where a { would be ambiguous (e.g., case value {)
    _non_block_expression: $ => choice(
      $.identifier,
      $.self_expression,
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
      $.if_expression,
      $.case_expression,
      $.concurrent_block,
      $.struct_construction,
      $.map_construction,
      $.anon_struct,
      $.struct_update,
      $.vector,
      $.set,
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

    // `3.14`, `1.5e3`, or an exponent with no fraction: `1e20`.
    float: $ => token(choice(
      /[0-9][0-9_]*\.[0-9][0-9_]*([eE][+-]?[0-9][0-9_]*)?/,
      /[0-9][0-9_]*[eE][+-]?[0-9][0-9_]*/,
    )),

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

    // A `..` with no tail (`[t, ..]`) is the bare spread: the list already at
    // the field or key the literal is written at.
    list: $ => seq('[', optional($._collection_items), ']'),

    paren_expression: $ => seq('(', $._expression, ')'),

    // A tuple, `#(a, b)`, or one that names a distinct type over a tuple:
    // `#Pair(1, "x")`.
    tuple_expression: $ => seq(
      choice('#(', seq('#', field('type', $._literal_head), '(')),
      $._expression,
      repeat1(seq(',', $._expression)),
      optional(','),
      ')',
    ),

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

    // Application field override, a statement: `with MyApp.logger = #Silent{}`.
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
    // scope. `Task.spawn(fn -> ...)` calls inside the block spawn tasks tied
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
      statements($),
      '}',
    ),

    case_expression: $ => prec(1, choice(
      // With scrutinee (`case x { pattern -> ... }`): arms match patterns.
      seq('case', $._non_block_expression, '{', repeat(seq($.case_branch, optional($._statement_end))), '}'),
      // Scrutinee-less (`case { guard -> ... }`): arms are boolean guard
      // EXPRESSIONS, not patterns. Splitting the two forms (rather than sharing
      // one branch with `choice(_case_pattern, _expression)`) keeps a guard
      // like `Float.is_nan(a)` from being parsed as a malformed `Type.Variant`
      // enum_pattern — there is no pattern alternative here to compete with it.
      // `_statement_end` ends an arm at a line break, so a body can't extend
      // into the next arm's pattern (`-1`, `.Some`, `[x]`, `#(a, b)`).
      seq('case', '{', repeat(seq($.guard_branch, optional($._statement_end))), '}'),
    )),

    case_branch: $ => choice(
      prec.dynamic(20, seq(
        $._case_pattern,
        optional(seq('when', $._expression)),
        '->',
        $._arm_body,
      )),
      // The expression escape: a pattern spelled as the expression it looks
      // like. A struct pattern reaches here, since the construction it spells
      // wins over the pattern at its fields, so its `as` is read as an
      // as_binding's is (`#.Rect{w, h} as r when w == h -> r`).
      seq(
        $._expression,
        optional(seq('when', $._expression)),
        '->',
        $._arm_body,
      ),
    ),

    // A scrutinee-less arm. A piped `|> case { Some(n) as m -> ... }` reaches
    // here too, so an arm's guard may be a pattern with a `when` (`Some(n)
    // when n > 0 -> ...`), and its `as` is read as an as_binding's is.
    guard_branch: $ => seq(
      $._expression,
      optional(seq('when', $._expression)),
      '->',
      $._arm_body,
    ),

    // What follows an arm's `->`: an expression, or an assertion
    // (`Err(e) -> assert e == "x"`), which is a statement and not an
    // expression elsewhere.
    _arm_body: $ => choice($.assertion, $._expression),

    _case_pattern: $ => prec(2, choice(
      $.identifier,
      $.type_identifier,
      // Qualified bare variant pattern: `Status.Open`, `Color.Red` (no
      // payload). Bare `Open` is the type_identifier alternative above; the
      // dotted form needs its own — `repeat1` requires a `.`, so it never
      // overlaps the bare one. (Payload forms are enum_pattern `Type.V(args)`
      // and struct_pattern `#Type.V{fields}`.)
      seq($.type_identifier, repeat1(seq(token.immediate('.'), $.type_identifier))),
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
      $.map_pattern,
      $.enum_pattern,
      $.list_pattern,
      $.vector_pattern,
      $.set_pattern,
      // String prefix pattern: "hello" + rest
      seq($.string, '+', $.identifier),
      // Negative literal: -1, -3.14, -1.50d
      seq('-', choice($.integer, $.float, $.decimal)),
      $.or_pattern,
      $.as_pattern,
      // Parentheses only group: `(.A(n) as a) or (.B(n) as a)`.
      seq('(', $._case_pattern, ')'),
    )),

    // `A or B`: either alternative. `or` binds looser than every pattern
    // construct but `as`, so `A or B as x` names the whole value.
    or_pattern: $ => prec.left(2, seq(
      field('alternative', $._case_pattern),
      'or',
      field('alternative', $._case_pattern),
    )),

    // `P as name`: P, and a name for the whole value it matched. `as` binds
    // looser than every other pattern construct. The name admits a
    // type_identifier as an import's `as` does: the compiler rejects one, and
    // with `identifier` alone tree-sitter shares the name's lex state with the
    // enum payload's same-line tokens, whose leading `[ \t]+` then lands in the
    // name's span.
    as_pattern: $ => prec.left(1, seq(
      field('pattern', $._case_pattern),
      'as',
      field('name', choice($.identifier, $.type_identifier)),
    )),

    // A struct pattern, `#{x, y: pat}`, or one that names its type or
    // variant: `#Point{x, y}`, `#Shape.Circle{radius}`, `#.Circle{radius}`.
    struct_pattern: $ => prec(11, choice(
      seq('#{', commaSep($.pattern_field), '}'),
      $._named_struct_pattern,
    )),

    _named_struct_pattern: $ => seq('#', field('type', $._literal_head), '{', commaSep($.pattern_field), '}'),

    // A map pattern, `%{"k" => pat}`, or one that names a map-distinct type
    // or a map-payload variant: `%Kvs{..}`, `%.Obj{"k" => v}`. Each key is an
    // expression, the key the map must hold.
    map_pattern: $ => prec(11, choice(
      seq('%{', commaSep($._map_pattern_entry), '}'),
      $._named_map_pattern,
    )),

    _named_map_pattern: $ => seq('%', field('type', $._literal_head), '{', commaSep($._map_pattern_entry), '}'),

    _map_pattern_entry: $ => seq($._expression, '=>', $._case_pattern),

    // A vector pattern, taken apart from either end: `#[first, ..rest]`,
    // `#[.., last]`, `#[a, ..mid, z]`, `#[a, b, ..]`, the exact `#[a, b]`, or
    // one that names its type: `#Path[a, ..]`, `#.Items[x]`. One `..` may
    // stand among the elements, with a name, `_` or nothing after it.
    vector_pattern: $ => prec(11, seq(
      choice('#[', seq('#', field('type', $._literal_head), '[')),
      optional(seq(
        choice($.pattern_spread, $._case_pattern),
        repeat(seq(',', choice($.pattern_spread, $._case_pattern))),
        optional(','),
      )),
      ']',
    )),

    // A set pattern tests membership: `%["admin", ..rest]` matches a set
    // holding "admin" and binds the other elements, `%[x, ..]` only tests,
    // and `%[1, 2]` with no `..` matches exactly that set. Its elements are
    // expressions, the values the set must hold, and `..` comes last; they
    // are read as a set literal's elements are, so `..rest` is a spread.
    set_pattern: $ => prec(11, seq(
      choice('%[', seq('%', field('type', $._literal_head), '[')),
      optional($._collection_items),
      ']',
    )),

    // The `..` of a vector pattern and what it binds: a name, `_`, or
    // nothing.
    pattern_spread: $ => seq('..', optional(choice($.identifier, '_'))),

    pattern_field: $ => choice(
      seq(field('name', $._field_identifier), ':', $._case_pattern),
      // A punned field. Its precedence is struct_construction's, so at the
      // `,` after `x` in `if #P{x, y} = p` neither reading wins statically and
      // both stay alive (the conflict above) until the `=` decides.
      prec(10, field('name', $._field_identifier)),
    ),

    // A tuple pattern, `#(a, b)`, or one that names a distinct type over a
    // tuple: `#Pair(a, b)`.
    tuple_pattern: $ => seq(
      choice('#(', seq('#', field('type', $._literal_head), '(')),
      $._case_pattern,
      repeat1(seq(',', $._case_pattern)),
      ')',
    ),

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
      // `self(p)`: the implementor taken apart in an interface default
      // bounded by `where self: Distinct<T>`.
      prec(11, seq($.self_type, '(', commaSep($._case_pattern), ')')),
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
    pipe_expression: $ => prec.left(6, seq($._expression, '|>', choice($.bare_assertion, $.assertion, $._expression, $.pipe_if_expression, $.then_stage, $.tap_stage))),
    // Error propagation. Prefix `try expr` unwraps one postfix-level operand;
    // bare `try` is also valid as a pipe stage (`value |> try`), where the
    // previous pipe value supplies the operand.
    try_expression: $ => prec.right(10, seq('try', optional(seq($._same_line, field('value', $._expression))))),
    dbg_expression: $ => prec.right(1, seq('dbg', optional(seq($._same_line, field('value', $._expression))))),
    // A placeholder for code not yet written: `todo` or `todo "reason"`. The
    // reason is a string literal; the compiler rejects an interpolated one.
    todo_expression: $ => prec.right(1, seq('todo', optional(seq($._same_line, field('reason', choice($.string, $.triple_string, $.raw_string)))))),

    // The name of a type built on a shape, written right after the shape's
    // symbol with no space: `#Point{..}`, `#geo.Point{..}`,
    // `#Shape.Circle{..}`, `#.Circle{..}`, `%Kvs{..}`, `#Path[..]`. Every
    // segment but the last names the owner, and `.Circle` is a variant of
    // the enum the position expects.
    _literal_head: $ => choice(
      alias($._immediate_dot_variant, $.dot_variant),
      seq(
        alias(token.immediate(/[A-Z][a-zA-Z0-9_]*/), $.type_identifier),
        repeat(seq(token.immediate('.'), $.type_identifier)),
      ),
      seq(
        alias(token.immediate(/[a-z_][a-zA-Z0-9_]*/), $.identifier),
        repeat1(seq(token.immediate('.'), $.type_identifier)),
      ),
    ),

    _immediate_dot_variant: $ => seq(token.immediate('.'), $.type_identifier),

    // A struct literal that names its type: `#Point{x: 1, y}`,
    // `#geo.Point{..}`, a struct-shaped variant `#Shape.Circle{radius: 1.0}`,
    // `#Circle{..}` or `#.Circle{..}`. A punned field (`#Point{x}`) means
    // `x: x`. The literal with no name is `anon_struct` (`#{x: 1}`).
    struct_construction: $ => prec.dynamic(10, prec(10, seq(
      '#',
      field('type', $._literal_head),
      '{',
      commaSep(choice(
        seq(field('field_name', $._field_identifier), ':', $._expression),
        field('field_name', $._field_identifier),
      )),
      '}',
    ))),

    // A map literal, `%{"a" => 1}` or the empty `%{}`, or one that names a
    // map-distinct type or a map-payload variant: `%Kvs{"a" => 1}`,
    // `%.Obj{..}`, `%Json.Obj{..}`. A map update, `%{..base, k => v}`, takes
    // its spread first; a `..` with no head (`%{.., k => v}`) is the map
    // already at the field or key the literal is written at.
    map_construction: $ => prec.dynamic(10, prec(10, choice(
      seq(
        '%{',
        choice(
          seq($.map_update_spread, repeat(seq(optional(','), $.map_entry)), optional(',')),
          commaSep($.map_entry),
        ),
        '}',
      ),
      seq('%', field('type', $._literal_head), '{', commaSep($.map_entry), '}'),
    ))),

    // A vector literal, `#[1, 2]`, `#[]`, with a spread first or last
    // (`#[..v, x]`, `#[x, ..v]`, the bare `#[.., x]`), or one that names a
    // vector-distinct type or a vector-payload variant: `#Path[1, 2]`,
    // `#.Items[1]`.
    vector: $ => prec.dynamic(10, prec(10, seq(
      choice('#[', seq('#', field('type', $._literal_head), '[')),
      optional($._collection_items),
      ']',
    ))),

    // A set literal, `%[1, 2]`, `%[]`, with a spread first (`%[..s, x]`, the
    // bare `%[.., x]`), or one that names a set-distinct type: `%Tags["a"]`.
    set: $ => prec.dynamic(10, prec(10, seq(
      choice('%[', seq('%', field('type', $._literal_head), '[')),
      optional($._collection_items),
      ']',
    ))),

    // The elements of a list, vector or set literal: expressions and `..`
    // spreads, comma-separated. A `..` with no head is the bare spread. Where
    // a spread may stand is the compiler's to check.
    _collection_items: $ => seq(
      choice(seq('..', $._expression), '..', $._expression),
      repeat(seq(',', choice(seq('..', $._expression), '..', $._expression))),
      optional(','),
    ),

    // A list attached to a list-distinct type or a list-payload enum
    // variant: `Items[1, 2, 3]`, `JV.Arr[...]`, `.Arr[...]`. A list has no
    // symbol, so the name stands before its bracket.
    typed_list: $ => prec.dynamic(10, prec(10, seq(
      choice($.type_identifier, $.type_field_access, $.dot_variant),
      $.list,
    ))),

    _callable_expression: $ => choice(
      $.identifier,
      $.self_expression,
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

    field_access: $ => choice(
      prec(12, seq($.type_identifier, token.immediate('.'), choice($._field_identifier, $.integer))),
      prec(11, seq($._expression, token.immediate('.'), choice($._field_identifier, $.integer))),
    ),

    // `self` in expression position: inside an interface default bounded by
    // `where self: Distinct<T>`, the implementor's constructor (`self(x)`,
    // or `self` as a function value). The analyzer rejects it elsewhere,
    // including as a field-access object (`self.method(x)`).
    self_expression: _ => 'self',
    type_field_access: $ => choice(
      prec(11, seq($.type_identifier, token.immediate('.'), $.type_identifier)),
      prec(11, seq($._expression, token.immediate('.'), $.type_identifier)),
    ),

    // Identifiers
    _field_identifier: $ => prec(1, $.identifier),
    identifier: $ => /[a-z_][a-zA-Z0-9_]*\??/,
    // `&1`, `&2`, ...: a parameter of the capture around it.
    placeholder: $ => /&[1-9][0-9]*/,

    // A capture: `&` against a function's body, which is one postfix-level
    // operand: a call (`&Int.max(&1, 0)`), a parenthesized expression
    // (`&(&1 > 2)`) or a literal (`&#Point{x: &1}`, `&#(&1, 0)`, `&[&1]`).
    // It binds below a call and above every binary operator, so
    // `&f(&1)(2)` captures both calls and `&f(&1) + 1` adds to the capture.
    capture: $ => prec(10, seq('&', field('body', choice(
      $.call_expression,
      $.paren_expression,
      $.struct_construction,
      $.anon_struct,
      $.struct_update,
      $.tuple_expression,
      $.list,
      $.typed_list,
      $.vector,
      $.set,
      $.map_construction,
    )))),
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

// The statements of a file or a block. A statement ends at a line break
// (`_statement_end`) or a semicolon, so a second statement on a statement's
// line without one is an error (`x = 1 2`, `p = Point{}`). The Go parser
// refuses only the second form, a statement right after another with nothing
// between them; the formatter never writes two statements on one line. An item led by `//!`
// prompts (`decorated_item`) needs no separator before it: a prompt starts its
// line, and the scanner emits it where the line's statement end would be. A
// block that starts its line follows `_newline_brace`, since the scanner
// leaves a line break before `{` to it: after a declaration's signature that
// brace opens the body.
//
// Each separator ends the run of items before it rather than starting the
// item after it, so no item's subtree starts with an empty token: an editor
// asking for the node at an item's first column (Neovim's get_node) finds
// the item, not the zero-width separator.
function statements($) {
  const separator = repeat1(choice(';', $._statement_end));
  const run = seq($._item, repeat($.decorated_item));
  return seq(
    repeat(';'),
    repeat(seq(run, choice(separator, $._newline_brace))),
    optional(run),
  );
}

// A type body: `{`, items one per line, `}`. As in the Go parser, an item
// ends at a line break (`_statement_end`) or a semicolon, and semicolons may
// stand anywhere between the braces, so a second item on an item's line
// without one is an error (`x: Int y: Int`). An item led by `//!` prompts
// (`decorated`) needs no separator before it: a prompt starts its line, and
// the scanner emits it where the line's statement end would be.
function typeBody($, item, decorated) {
  const separator = repeat1(choice(';', $._statement_end));
  const next = decorated
    ? choice(seq(separator, choice(item, decorated)), decorated)
    : seq(separator, item);
  const first = decorated ? choice(item, decorated) : item;
  return seq(
    '{',
    repeat(';'),
    optional(seq(first, repeat(next), optional(separator))),
    '}',
  );
}

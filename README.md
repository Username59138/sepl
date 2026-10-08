# SEPL

Simple Easy Programming Language: an interpreter written in Go.
Source → lexer → parser → bytecode compiler → stack virtual machine.

Install (Go 1.24 or newer):

```
go install github.com/Username59138/sepl/cmd/sepl@latest
```

Or build from a clone:

```
go build -o sepl ./cmd/sepl
./sepl examples/primes.sepl 100   # run a program; main(args) gets the arguments
./sepl examples/snake.sepl        # the terminal snake game (arrow keys)
./sepl examples/shapes.sepl       # enums, match and ?
./sepl examples/modules/main.sepl # a program in several files, with type annotations
./sepl                            # REPL: type code, see results
./sepl dis examples/fact.sepl     # show the bytecode
./sepl parse examples/snake.sepl  # show the syntax tree
./sepl lex examples/snake.sepl    # show the tokens
go test ./...                     # tests
go test -bench . ./interp         # benchmarks (programs in bench/)
```

## Status

Works now:

- numbers (`int` 64-bit with overflow errors, `float`), `bool`, `nil`, `str`, `list`, `map`, ranges `a..b`
- `/` always gives a float, `//` and `%` round toward negative infinity (like Python)
- no implicit conversions: `"5" + 1` is an error; `str()`, `int()`, `float()` convert
- `let`, `const`, block scopes, closures (each loop iteration gets its own variable)
- `if` and `match` as statements and expressions; match on values, several values, ranges, `_`
- `while`, `for ... in` over lists, strings, maps, ranges; `break`, `continue`
- functions, recursion, named and variadic (`...rest`) arguments, functions and methods as values
- f-strings, `+=` and friends, `in` / `not in`
- top-level code runs first, then `main(args)`; an int returned by `main` is the exit code
- structs: `let` fields (defaults are computed for every new object), `const x` (given at creation,
  then fixed), `const X = v` (one per type, `Foo::X`); objects are created by field name: `point(x: 1, y: 2)`
- `impl`: methods with `self`, static methods without it (`Foo::create()`), required methods without a body
- inheritance: `struct c is a, b`; `add struct s to t` (fields), `add impl s to t[: overrides]` (methods);
  the same field or method reached twice from one origin is fine, two different ones are an error until
  `c` declares its own; `a.method(self)` calls a specific parent's version
- traits: operators and protocols are methods. `+ - * / // %` call `plus minus mul div idiv mod`,
  `-x` calls `neg`, `==` calls `eq` (otherwise objects compare field by field), `<` calls `lt`
  (which also gives `> <= >=` and sorting), `x[i]` / `x[i] = v` call `index` / `set_index`,
  `in` calls `contains`, `len()` calls `len`, `for` calls `iter`, `print` and f-strings call `to_str`.
  The standard traits are declared in SEPL itself (`interp/prelude.sepl`).
- methods on builtin types: `impl int: fn double(self): ...`, `add impl shown to bool`
- enums: variants with or without data (`shape::circle(5)`, `shape::rect(w: 2, h: 3)`, `shape::empty`),
  methods and traits like structs, `enum net_err is io_err` (an inherited variant is the same variant),
  variants without data work as map keys
- `match` takes enum values apart: `shape::circle(r)`, nested `result::ok(shape::rect(w, 0))`,
  `_` inside, and `result::err` without parentheses matches every error
- errors are values: `result::ok(v)` / `result::err(msg)` and `option::some(v)` / `option::none` with
  `unwrap`, `unwrap_or`, `is_ok`...; `x?` calls `x.try()`, which says (through `flow::next` / `flow::exit`)
  whether to go on with the value or return it; any type can implement `try`. `panic(msg)` stops the program
- type annotations are checked while the program runs: `let x int = 5`, `let x := 5` (the type of the
  first value), parameters (`fn f(a int, ...rest str) float`), results (also when `?` returns), struct and
  variant fields. `nil` fits every type; an int fits `float` and becomes a float; a struct fits its parents
  and the traits it took with `is` / `add impl`; an enum value fits an enum that has its variant. Code
  without annotations runs exactly as before; a wrong argument is reported where the call is
- modules: `import geo` loads `geo.sepl` next to the importing file, `import shapes::geometry` loads
  `shapes/geometry.sepl`, `import geo as g` renames it; then `geo::point`, `geo::dist(p)`. Top-level names
  are exported except those starting with `_`; a module runs once, its `main` is not called; circular
  imports are an error. Without such a file, the built-in module of that name is used
- built-in modules: `term` (`key wait_key clear sleep hide_cursor show_cursor`), `random`
  (`int float choice shuffle seed`), `math`, `time`; `print(x, pos: p)` draws at a terminal position
- builtins: `print len str int float bool list map range type input abs min max exit panic`
- errors with the source line, a caret and the chain of calls

Next: macros.

## Speed

Best of 5 runs on the sandbox this was built in (2 cores), against CPython 3.13:

| Program (`bench/`) | SEPL | Python |
| --- | --- | --- |
| `fib.sepl`: recursive fib(30) | 0.12 s | 0.13 s |
| `loop.sepl`: 10M loop iterations with `%` and `+=` | 0.25 s | 0.49 s |
| `lists.sepl`: 1M `push`, map counting | 0.27 s | 0.14 s |

What makes it fast: values are a small struct (numbers never allocate), int fast paths,
fused instructions (`if a < b` compiles to one compare-and-jump, `x += 1` to one instruction,
`n - 1` keeps the 1 inside the instruction), `for i in a..b` counts without a range object,
method names are resolved to table indexes at compile time, and the stack grows on demand so
garbage collection stays cheap. List-heavy code is still slower than Python: growing big
lists copies 32-byte values; shrinking values to 16 bytes is the next big speed-up.

## Layout

| Path | What |
| --- | --- |
| `token/` | token types, keywords, positions |
| `lexer/` | source → tokens, including `NEWLINE` / `INDENT` / `DEDENT` |
| `ast/` | syntax tree; `ast.String` / `ast.Pretty` print it |
| `parser/` | tokens → syntax tree, with error recovery |
| `compiler/` | syntax tree → bytecode; tracks the exact stack height, so locals are stack slots |
| `vm/` | values, bytecode format and disassembler, the VM loop, builtins and methods |
| `interp/` | runs files and REPL input, formats errors |
| `diag/` | error messages with the source line and a caret |
| `cmd/sepl/` | command-line tool |
| `examples/`, `bench/` | sample programs and benchmarks |

## Syntax rules worth knowing

- A newline ends a statement unless the line ends with an operator or a comma, or is inside brackets.
- Indentation is spaces only. A block is `:` plus indented lines, or one statement on the same line.
- Comparisons do not chain (`a < b < c` is an error); positional arguments come before named ones.
- `add`, `to`, `is` are contextual words, so `math::add(x, y)` still works.

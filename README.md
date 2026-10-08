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
./sepl examples/stdlib.sepl users.json  # files, JSON, regex, time
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
- `type(x)` gives the type itself: `type(1) == int`, `type(p) == point`, `type(int) == type`; it prints as
  its name (`int`), and comparing it with a str (`type(x) == "int"`) is an error that says what to write
- an annotation can be any expression that gives a type: `let y type(x) = ...`, `fn same(a, b type(a))`,
  `let s types[0]`; it is computed where a named type would be looked up
- `let`, `const`, block scopes, closures (each loop iteration gets its own variable)
- `if` and `match` as statements and expressions; match on values, several values, ranges, `_`
- `while`, `for ... in` over lists, strings, maps, ranges; `break`, `continue`
- functions, recursion, named and variadic (`...rest`) arguments, functions and methods as values
- anonymous functions: `fn(x): x * 2` returns its expression (`list.map(fn(x): x * x)`, `fn(a): fn(b): a + b`);
  with an indented body they work like named functions and need `return`. Parameters and result can be typed
- f-strings, `+=` and friends, `in` / `not in`
- top-level code runs first, then `main(args)`; an int returned by `main` is the exit code
- structs: `let` fields (defaults are computed for every new object), `const x` (given at creation,
  then fixed), `const X = v` (one per type, `Foo::X`); objects are created by field name: `point(x: 1, y: 2)`
- `impl`: methods with `self`, static methods without it (`Foo::create()`), required methods without a body
- inheritance: `struct c is a, b`; `add struct s to t` (fields), `add impl s to t[: overrides]` (methods);
  the same field or method reached twice from one origin is fine, two different ones are an error until
  `c` declares its own; `a.method(self)` calls a specific parent's version
- `add struct` / `add impl` work anywhere a statement does (a function, a loop, after `:` on one line); fields
  added to a struct also reach the structs that already inherit from it, and objects created earlier get them
  with their defaults
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
- the standard library below; `print(x, pos: p)` draws at a terminal position
- builtins: `print len str int float bool list map range type input abs min max exit panic`
- methods: `str` (`upper lower trim split lines contains starts_with ends_with find count replace repeat
  chars slice pad_left pad_right is_digit is_alpha is_space`), `list` (`push pop insert contains index_of
  count reverse sort sort(key) join clear copy slice map filter reduce sum any all find enumerate`),
  `map` (`keys values items contains get remove clear copy`), `range` (`contains to_list`), `to_str` on anything
- errors with the source line, a caret and the chain of calls

Next: macros.

## Standard library

Small on purpose. Anything that can fail for outside reasons returns a `result` (or an `option`);
mistakes in the program itself (a bad regex, a wrong argument type) are runtime errors.

| Module | Functions |
| --- | --- |
| `fs` | `read(path)`, `write(path, text)`, `append(path, text)`, `exists`, `is_dir`, `list(dir)`, `mkdir` (with parents), `remove`, `join(a, b, ...)` |
| `os` | `env(name)` → option, `cwd()`, `platform`, `run(cmd, ...args)` → result with the output |
| `json` | `parse(text)` → result (objects keep key order, numbers are int or float), `to_str(value, indent: 2)` (structs become objects) |
| `re` | `matches(p, s)`, `find` → option, `find_all`, `groups` → option, `replace(p, s, with)` (`$1` for groups), `split` |
| `math` | `pi e inf`, `sqrt sin cos tan asin acos atan atan2 hypot log log10 log2 exp pow`, `floor ceil round(x[, digits])`, `clamp`, `is_nan` |
| `random` | `int(lo..hi)` / `int(lo, hi)`, `float()`, `choice`, `shuffle`, `seed` |
| `time` | `now()` (seconds), `sleep(ms)`, `format([t, layout])` with `%Y %m %d %H %M %S` |
| `term` | `key()` (nil if no key), `wait_key()`, `clear()`, `sleep(ms)`, `hide_cursor()`, `show_cursor()` |

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

## License

SEPL is free software under the GNU General Public License v3.0; see [LICENSE](LICENSE).

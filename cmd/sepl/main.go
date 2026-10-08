// Command sepl is the SEPL interpreter.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/Username59138/sepl/ast"
	"github.com/Username59138/sepl/diag"
	"github.com/Username59138/sepl/interp"
	"github.com/Username59138/sepl/lexer"
	"github.com/Username59138/sepl/parser"
	"github.com/Username59138/sepl/token"
	"github.com/Username59138/sepl/vm"
)

const usage = `SEPL — Simple Easy Programming Language

Usage:
  sepl                      start the REPL
  sepl <file> [args...]     run a program; main(args) gets the arguments
  sepl dis <file>           show the bytecode of a program
  sepl parse <file>         show the syntax tree of a program
  sepl lex <file>           show the tokens of a program
`

func main() {
	// Programs allocate many small values; collecting garbage less often
	// makes them 2-3 times faster for a modest amount of extra memory.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(400)
	}
	args := os.Args[1:]
	switch {
	case len(args) == 0:
		repl()
	case args[0] == "-h" || args[0] == "--help" || args[0] == "help":
		fmt.Print(usage)
	case args[0] == "lex" && len(args) == 2:
		os.Exit(lexFile(args[1]))
	case args[0] == "parse" && len(args) == 2:
		os.Exit(parseFile(args[1]))
	case args[0] == "dis" && len(args) == 2:
		os.Exit(disFile(args[1]))
	default:
		os.Exit(runFile(args[0], args[1:]))
	}
}

func readFile(path string) (string, bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sepl:", err)
		return "", false
	}
	return string(src), true
}

func report(err error) {
	if e, ok := err.(*interp.Error); ok {
		fmt.Fprint(os.Stderr, e.Format())
		return
	}
	fmt.Fprintln(os.Stderr, "sepl:", err)
}

func runFile(path string, args []string) int {
	src, ok := readFile(path)
	if !ok {
		return 1
	}
	s := interp.NewSession(nil, nil)
	// Ctrl+C: put the terminal back the way it was (term::key changes it).
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		s.Close()
		os.Exit(130)
	}()
	code, err := s.RunFile(path, src, args)
	s.Close()
	if err != nil {
		if _, isExit := err.(*vm.ExitError); !isExit {
			report(err)
		}
	}
	return code
}

func disFile(path string) int {
	src, ok := readFile(path)
	if !ok {
		return 1
	}
	out, err := interp.NewSession(nil, nil).Disasm(path, src)
	if err != nil {
		report(err)
		return 1
	}
	fmt.Print(out)
	return 0
}

func parseFile(path string) int {
	src, ok := readFile(path)
	if !ok {
		return 1
	}
	prog, err := parser.Parse(src)
	if err != nil {
		for _, e := range err.(parser.ErrorList) {
			fmt.Fprint(os.Stderr, diag.Format(path, src, e.Pos, e.Msg))
		}
		return 1
	}
	fmt.Println(ast.Pretty(prog))
	return 0
}

func lexFile(path string) int {
	src, ok := readFile(path)
	if !ok {
		return 1
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	l := lexer.New(src)
	depth := 0
	for {
		t := l.Next()
		if t.Type == token.ILLEGAL {
			out.Flush()
			e := l.Err()
			fmt.Fprint(os.Stderr, diag.Format(path, src, e.Pos, e.Msg))
			return 1
		}
		if t.Type == token.DEDENT {
			depth--
		}
		fmt.Fprintf(out, "%-8s %s%s\n", t.Pos, strings.Repeat("  ", depth), t)
		if t.Type == token.INDENT {
			depth++
		}
		if t.Type == token.EOF {
			return 0
		}
	}
}

// repl runs what you type. A line ending with ':' starts a block: keep typing
// indented lines and finish it with an empty line.
func repl() {
	fmt.Println("SEPL REPL. Ctrl+D to exit.")
	s := interp.NewSession(nil, nil)
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(">>> ")
		if !in.Scan() {
			fmt.Println()
			return
		}
		src := in.Text()
		if strings.TrimSpace(src) == "" {
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(src), ":") {
			for {
				fmt.Print("... ")
				if !in.Scan() {
					break
				}
				line := in.Text()
				if strings.TrimSpace(line) == "" {
					break
				}
				src += "\n" + line
			}
		}
		v, err := s.Eval(src)
		if err != nil {
			if e, ok := err.(*vm.ExitError); ok {
				os.Exit(e.Code)
			}
			report(err)
			continue
		}
		if !v.IsNil() {
			fmt.Println(s.VM.ReprValue(v))
		}
	}
}

package vm

// Env holds the global variables of one module: a file, or the REPL. Every
// compiled function points to the Env of the module it was written in.
type Env struct {
	Name   string
	Names  []string
	Values []Value
}

func NewEnv(name string) *Env { return &Env{Name: name} }

// Add makes room for a new global and returns its index.
func (e *Env) Add(name string) int {
	e.Names = append(e.Names, name)
	e.Values = append(e.Values, undef)
	return len(e.Names) - 1
}

// Get returns global i and whether it has been defined.
func (e *Env) Get(i int) (Value, bool) {
	if i < 0 || i >= len(e.Values) || e.Values[i].K == kUndef {
		return Nil, false
	}
	return e.Values[i], true
}

// Set defines global i.
func (e *Env) Set(i int, v Value) { e.Values[i] = v }

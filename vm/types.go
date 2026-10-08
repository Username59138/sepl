package vm

import (
	"slices"
	"sort"
	"strings"
)

// ---------------------------------------------------------------- descriptors
//
// The compiler puts these in the constant table; the VM builds the runtime
// objects from them.

// FieldMode says how a struct field gets its value.
type FieldMode uint8

const (
	FieldNil      FieldMode = iota // let x: nil unless given
	FieldLiteral                   // let x = 5: a constant default
	FieldThunk                     // let x = [...]: a default computed for every new object
	FieldRequired                  // const x: must be given when creating the object
	FieldStatic                    // const X = v: one value for the whole type (Foo::X)
)

// FieldDesc describes one field of a struct declaration.
type FieldDesc struct {
	Name    string
	Mode    FieldMode
	Literal Value
	Typed   bool // its type is on the stack
	Infer   bool // let x := v: the type of the default value becomes the field's type
}

// StructDesc describes a struct declaration. Before OpStruct the stack holds
// the parent types, then for each field in order: its value if FieldStatic,
// its default function if FieldThunk, and its type if Typed.
type StructDesc struct {
	Name     string
	NParents int
	Fields   []FieldDesc
}

func (*StructDesc) TypeName() string { return "desc" }

// Pushed is how many values OpStruct takes from the stack.
func (d *StructDesc) Pushed() int {
	n := d.NParents
	for _, f := range d.Fields {
		if f.Mode == FieldThunk || f.Mode == FieldStatic {
			n++
		}
		if f.Typed {
			n++
		}
	}
	return n
}

// MethodDesc describes a method in impl or add impl. Abstract methods have
// no body; static methods have no self.
type MethodDesc struct {
	Name     string
	Abstract bool
	Static   bool
}

// ImplDesc lists the methods of an impl block; the closures of the
// non-abstract ones are on the stack, in order.
type ImplDesc struct {
	Methods []MethodDesc
}

func (*ImplDesc) TypeName() string { return "desc" }

// Bodies is how many closures the impl block pushes.
func (d *ImplDesc) Bodies() int {
	n := 0
	for _, m := range d.Methods {
		if !m.Abstract {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- runtime objects

// TypeObj is a type: a struct declared in SEPL, or a builtin type such as
// int whose methods SEPL code can extend.
type TypeObj struct {
	Name     string
	builtin  bool
	ctor     func(vm *VM, args []Value, kw []Kwarg) (Value, error) // builtin types
	Fields   []*FieldInfo
	fieldIdx map[string]int
	statics  map[string]Value
	own      map[string]*MethodInfo
	bases    []*TypeObj // parents (is) and sources of add impl, searched for methods
	supers   []*TypeObj // sources of add struct (for type checks)
	children []*TypeObj // types that copied this one's fields (is, add struct): they get its later fields too
	extended bool       // SEPL code added methods (matters for builtin types)
	sealed   bool       // its values never look methods up (nil, fn, type, module): impl and add refuse it

	isEnum       bool
	variants     map[string]*VariantInfo
	variantOrder []string
	singletons   map[string]Value // values of variants without data

	cache      map[string]lookupResult
	cacheEpoch int
	checkEpoch int // epoch at which the type was found complete (no missing methods)
	checkErr   error
}

func (*TypeObj) TypeName() string { return "type" }

// FieldInfo is a field of a struct type.
type FieldInfo struct {
	Name    string
	Mode    FieldMode
	Literal Value
	Thunk   Value
	Type    Value    // nil: any type
	Infer   bool     // the type is that of the default value; Type is set once it is known
	Origin  *TypeObj // the struct that declared it first
}

// MethodInfo is a method. Fn is nil for a required (abstract) method.
type MethodInfo struct {
	Name   string
	Fn     Value
	Static bool
	Origin *TypeObj // the type whose impl defined it
}

func (m *MethodInfo) abstract() bool { return m.Fn.K == KNil }

type lookupResult struct {
	m   *MethodInfo
	err error
}

// Instance is an object of a struct type.
type Instance struct {
	Type   *TypeObj
	Fields []Value
}

func (i *Instance) TypeName() string { return i.Type.Name }

// BoundMethod is obj.method taken without calling it.
type BoundMethod struct {
	Recv Value
	Fn   Value // nil for a method implemented in Go
	Name string
}

func (*BoundMethod) TypeName() string { return "fn" }

func newType(name string) *TypeObj {
	return &TypeObj{
		Name:     name,
		fieldIdx: map[string]int{},
		statics:  map[string]Value{},
		own:      map[string]*MethodInfo{},
		// -1: never checked
		checkEpoch: -1,
		cacheEpoch: -1,
	}
}

func (t *TypeObj) addField(f *FieldInfo) {
	if i, ok := t.fieldIdx[f.Name]; ok {
		t.Fields[i] = f
		return
	}
	t.fieldIdx[f.Name] = len(t.Fields)
	t.Fields = append(t.Fields, f)
}

func (t *TypeObj) kind() string {
	switch {
	case t.builtin:
		return "type"
	case t.isEnum:
		return "enum"
	}
	return "struct"
}

// ---------------------------------------------------------------- declarations

func asStruct(v Value, what string) (*TypeObj, error) {
	t, ok := v.O.(*TypeObj)
	if !ok {
		return nil, errorf("%s needs a struct, not %s", what, TypeName(v))
	}
	if t.builtin {
		return nil, errorf("%s needs a struct; %s is a builtin type", what, t.Name)
	}
	return t, nil
}

// sealedErr refuses methods for types whose values never look them up.
func sealedErr(t *TypeObj) error {
	if t.sealed {
		return errorf("cannot add methods to %s: its values have no methods", t.Name)
	}
	return nil
}

func asType(v Value, what string) (*TypeObj, error) {
	t, ok := v.O.(*TypeObj)
	if !ok {
		return nil, errorf("%s needs a type, not %s", what, TypeName(v))
	}
	return t, nil
}

// makeStruct builds a struct type: inherited fields first, then its own.
func (vm *VM) makeStruct(d *StructDesc, parents, vals []Value) (Value, error) {
	t := newType(d.Name)
	own := map[string]bool{}
	for _, f := range d.Fields {
		own[f.Name] = true
	}
	for _, pv := range parents {
		p, err := asStruct(pv, "struct "+d.Name+" is ...")
		if err != nil {
			return Nil, err
		}
		t.bases = append(t.bases, p)
		p.children = append(p.children, t)
		for _, f := range p.Fields {
			if own[f.Name] {
				continue // redeclared: the struct's own field wins
			}
			if i, ok := t.fieldIdx[f.Name]; ok {
				if t.Fields[i].Origin != f.Origin {
					return Nil, errorf("struct %s gets field '%s' from both %s and %s; declare it in %s to choose",
						d.Name, f.Name, t.Fields[i].Origin.Name, f.Origin.Name, d.Name)
				}
				continue // the same field through two paths (diamond)
			}
			t.addField(f)
		}
		for k, v := range p.statics {
			if !own[k] {
				if _, ok := t.statics[k]; !ok {
					t.statics[k] = v
				}
			}
		}
	}
	vi := 0
	next := func() Value {
		v := vals[vi]
		vi++
		return v
	}
	for _, fd := range d.Fields {
		var static, thunk, typ Value
		if fd.Mode == FieldStatic {
			static = next()
		}
		if fd.Mode == FieldThunk {
			thunk = next()
		}
		if fd.Typed {
			typ = next()
		}
		ctx := "field '" + fd.Name + "' of " + d.Name
		switch fd.Mode {
		case FieldStatic:
			v, err := vm.checkType(static, typ, d.Name+"::"+fd.Name)
			if err != nil {
				return Nil, err
			}
			t.statics[fd.Name] = v
			if i, ok := t.fieldIdx[fd.Name]; ok { // a parent field with this name
				t.Fields = append(t.Fields[:i], t.Fields[i+1:]...)
				t.fieldIdx = map[string]int{}
				for j, f := range t.Fields {
					t.fieldIdx[f.Name] = j
				}
			}
			continue
		case FieldThunk:
			// With :=, the type is learned from the first default computed.
			t.addField(&FieldInfo{Name: fd.Name, Mode: fd.Mode, Thunk: thunk, Type: typ, Infer: fd.Infer, Origin: t})
		default:
			lit, err := vm.checkType(fd.Literal, typ, "the default of "+ctx)
			if err != nil {
				return Nil, err
			}
			if fd.Infer {
				if typ, err = vm.typeOfValue(lit, "field '"+fd.Name+"'"); err != nil {
					return Nil, err
				}
			}
			t.addField(&FieldInfo{Name: fd.Name, Mode: fd.Mode, Literal: lit, Type: typ, Origin: t})
		}
		delete(t.statics, fd.Name)
	}
	return Obj(t), nil
}

var operatorMethods = map[string]bool{
	"plus": true, "minus": true, "mul": true, "div": true, "idiv": true, "mod": true,
	"neg": true, "eq": true, "lt": true,
}

// installMethods puts methods into t's own table.
func (vm *VM) installMethods(t *TypeObj, d *ImplDesc, fns []Value) {
	j := 0
	for _, md := range d.Methods {
		m := &MethodInfo{Name: md.Name, Static: md.Static, Origin: t}
		if !md.Abstract {
			m.Fn = fns[j]
			j++
		}
		t.own[md.Name] = m
		if t.builtin && operatorMethods[md.Name] && (t == vm.tInt || t == vm.tFloat) {
			vm.numOps = true
		}
	}
	t.extended = true
	vm.epoch++
}

// implement runs "impl T: methods".
func (vm *VM) implement(target Value, d *ImplDesc, fns []Value) error {
	t, err := asType(target, "impl")
	if err != nil {
		return err
	}
	if err := sealedErr(t); err != nil {
		return err
	}
	vm.installMethods(t, d, fns)
	return nil
}

// addStruct runs "add struct S to T": T gets S's fields, and so does every
// type that already inherits T's fields (struct c is T, add struct T to c),
// however deep. Nothing changes if any of them would get a conflicting field.
// Existing objects get the new fields (with their defaults) the first time
// they are used.
func (vm *VM) addStruct(src, dst Value) error {
	s, err := asStruct(src, "add struct")
	if err != nil {
		return err
	}
	t, ok := dst.O.(*TypeObj)
	if !ok {
		return errorf("add struct ... to needs a struct, not %s", TypeName(dst))
	}
	if t.builtin {
		return errorf("cannot add fields to the builtin type %s", t.Name)
	}
	if s == t {
		return errorf("cannot add struct %s to itself", s.Name)
	}
	// Plan first: which fields each type gets.
	type plan struct {
		t      *TypeObj
		fields []*FieldInfo
	}
	var plans []plan
	visited := map[*TypeObj]bool{}
	var walk func(u *TypeObj, fields []*FieldInfo) error
	walk = func(u *TypeObj, fields []*FieldInfo) error {
		if visited[u] {
			return nil
		}
		visited[u] = true
		var got []*FieldInfo
		for _, f := range fields {
			if i, ok := u.fieldIdx[f.Name]; ok {
				have := u.Fields[i].Origin
				if have == f.Origin || u != t && have == u {
					continue // already there, or redeclared by a child: its own field wins
				}
				if u == t {
					return errorf("%s already has a field '%s' (from %s)", u.Name, f.Name, have.Name)
				}
				return errorf("cannot add struct %s to %s: %s (a child of %s) already has a field '%s' (from %s); declare it in %s to choose",
					s.Name, t.Name, u.Name, t.Name, f.Name, have.Name, u.Name)
			}
			if _, ok := u.statics[f.Name]; ok && u != t {
				continue // the child made it a type constant
			}
			got = append(got, f)
		}
		if len(got) == 0 {
			return nil
		}
		plans = append(plans, plan{u, got})
		for _, c := range u.children {
			if err := walk(c, got); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(t, s.Fields); err != nil {
		return err
	}
	for _, p := range plans {
		for _, f := range p.fields {
			p.t.addField(f)
		}
		for k, v := range s.statics {
			if _, ok := p.t.statics[k]; !ok {
				p.t.statics[k] = v
			}
		}
	}
	t.supers = append(t.supers, s)
	if !slices.Contains(s.children, t) {
		s.children = append(s.children, t)
	}
	vm.epoch++
	return nil
}

// addImpl runs "add impl S to T[: overrides]": T gets S's methods. A method
// T already has from somewhere else is a conflict unless the block overrides it.
func (vm *VM) addImpl(src, dst Value, d *ImplDesc, fns []Value) error {
	s, err := asStruct(src, "add impl")
	if err != nil {
		return err
	}
	t, err := asType(dst, "add impl ... to")
	if err != nil {
		return err
	}
	if err := sealedErr(t); err != nil {
		return err
	}
	if s == t {
		return errorf("cannot add impl %s to itself", s.Name)
	}
	overridden := map[string]bool{}
	for _, md := range d.Methods {
		overridden[md.Name] = true
	}
	for _, name := range vm.methodNames(s) {
		if overridden[name] {
			continue
		}
		sm, err := vm.lookup(s, name)
		if err != nil {
			return err
		}
		tm, err := vm.lookup(t, name)
		if err != nil || tm == nil || sm == nil || tm == sm || sm.abstract() || tm.abstract() || tm.Origin == t {
			continue
		}
		return errorf("%s already has method '%s' (from %s); override it in 'add impl %s to %s:'",
			t.Name, name, tm.Origin.Name, s.Name, t.Name)
	}
	t.bases = append(t.bases, s)
	if t.builtin {
		for _, name := range vm.methodNames(s) {
			if operatorMethods[name] && (t == vm.tInt || t == vm.tFloat) {
				vm.numOps = true
			}
		}
	}
	vm.installMethods(t, d, fns)
	return nil
}

// ---------------------------------------------------------------- method lookup

// lookup finds a method by name: the type's own methods first, then its
// bases. A required method of one base is satisfied by another base's
// implementation; two different implementations are an error.
func (vm *VM) lookup(t *TypeObj, name string) (*MethodInfo, error) {
	if t.cacheEpoch != vm.epoch {
		t.cache = map[string]lookupResult{}
		t.cacheEpoch = vm.epoch
	}
	if r, ok := t.cache[name]; ok {
		return r.m, r.err
	}
	m, err := resolveMethod(t, name, 0)
	t.cache[name] = lookupResult{m, err}
	return m, err
}

func resolveMethod(t *TypeObj, name string, depth int) (*MethodInfo, error) {
	if depth > 64 {
		return nil, errorf("the types that %s is built from refer to each other in a loop", t.Name)
	}
	own := t.own[name]
	if own != nil && !own.abstract() {
		return own, nil
	}
	var found *MethodInfo
	for _, b := range t.bases {
		m, err := resolveMethod(b, name, depth+1)
		if err != nil {
			return nil, err
		}
		switch {
		case m == nil || m == found:
		case found == nil:
			found = m
		case m.abstract():
		case found.abstract():
			found = m
		default:
			return nil, errorf("%s gets method '%s' from both %s and %s; define it in 'impl %s:' to choose",
				t.Name, name, found.Origin.Name, m.Origin.Name, t.Name)
		}
	}
	if found == nil || (found.abstract() && own != nil) {
		return own, nil
	}
	return found, nil
}

// methodNames lists every method name visible in t, sorted.
func (vm *VM) methodNames(t *TypeObj) []string {
	seen := map[string]bool{}
	var walk func(*TypeObj, int)
	walk = func(t *TypeObj, depth int) {
		if depth > 64 {
			return
		}
		for n := range t.own {
			seen[n] = true
		}
		for _, b := range t.bases {
			walk(b, depth+1)
		}
	}
	walk(t, 0)
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// checkComplete makes sure objects of t can be created: no required method
// without a body and no conflicting methods.
func (vm *VM) checkComplete(t *TypeObj) error {
	if t.checkEpoch == vm.epoch {
		return t.checkErr
	}
	t.checkEpoch = vm.epoch
	t.checkErr = nil
	for _, name := range vm.methodNames(t) {
		m, err := vm.lookup(t, name)
		if err != nil {
			t.checkErr = errorf("cannot create %s: %s", t.Name, err)
			break
		}
		if m != nil && m.abstract() {
			t.checkErr = errorf("cannot create %s: the required method '%s' (from %s) has no body; write it in 'impl %s:'",
				t.Name, name, m.Origin.Name, t.Name)
			break
		}
	}
	return t.checkErr
}

// ---------------------------------------------------------------- objects

// construct creates an object: point(x: 1, y: 2).
func (vm *VM) construct(t *TypeObj, args []Value, kw []Kwarg) (Value, error) {
	if t.ctor != nil {
		return t.ctor(vm, args, kw)
	}
	if t.isEnum {
		return Nil, errorf("%s is an enum: make its values with %s::variant", t.Name, t.Name)
	}
	if len(args) > 0 {
		example := "x: 1"
		if len(t.Fields) > 0 {
			example = t.Fields[0].Name + ": ..."
		}
		return Nil, errorf("fields are set by name: %s(%s)", t.Name, example)
	}
	if err := vm.checkComplete(t); err != nil {
		return Nil, err
	}
	inst := &Instance{Type: t, Fields: make([]Value, len(t.Fields))}
	set := make([]bool, len(t.Fields))
	// A ':=' field whose default is computed gets its type the first time:
	// compute the default now, so a value given here can be checked too.
	var computed map[int]Value
	for i, f := range t.Fields {
		if f.Infer && f.Type.K == KNil {
			v, err := vm.fieldDefault(f)
			if err != nil {
				return Nil, err
			}
			typ, err := vm.typeOfValue(v, "field '"+f.Name+"' of "+t.Name)
			if err != nil {
				return Nil, err
			}
			f.Type = typ
			if computed == nil {
				computed = map[int]Value{}
			}
			computed[i] = v
		}
	}
	for _, k := range kw {
		i, ok := t.fieldIdx[k.Name]
		if !ok {
			if _, isStatic := t.statics[k.Name]; isStatic {
				return Nil, errorf("%s is a constant of the type (%s::%s), not a field", k.Name, t.Name, k.Name)
			}
			return Nil, errorf("%s has no field '%s'", t.Name, k.Name)
		}
		v, err := vm.checkType(k.Value, t.Fields[i].Type, "field '"+k.Name+"' of "+t.Name)
		if err != nil {
			return Nil, err
		}
		inst.Fields[i], set[i] = v, true
	}
	for i, f := range t.Fields {
		if set[i] {
			continue
		}
		if f.Mode == FieldRequired {
			return Nil, errorf("missing field '%s': const fields must be given when creating %s", f.Name, t.Name)
		}
		if v, ok := computed[i]; ok {
			inst.Fields[i] = v
			continue
		}
		v, err := vm.fieldDefault(f)
		if err != nil {
			return Nil, err
		}
		if f.Mode == FieldThunk {
			if v, err = vm.checkType(v, f.Type, "the default of field '"+f.Name+"' of "+t.Name); err != nil {
				return Nil, err
			}
		}
		inst.Fields[i] = v
	}
	return Obj(inst), nil
}

func (vm *VM) fieldDefault(f *FieldInfo) (Value, error) {
	switch f.Mode {
	case FieldLiteral:
		return f.Literal, nil
	case FieldThunk:
		return vm.Call(f.Thunk, nil)
	}
	return Nil, nil
}

// fillStale migrates v if it is an object created before add struct gave
// its type more fields.
func (vm *VM) fillStale(v Value) error {
	if inst, ok := v.O.(*Instance); ok && len(inst.Fields) < len(inst.Type.Fields) {
		return vm.migrate(inst)
	}
	return nil
}

// migrate gives an object the fields added to its type by add struct.
func (vm *VM) migrate(inst *Instance) error {
	for i := len(inst.Fields); i < len(inst.Type.Fields); i++ {
		v, err := vm.fieldDefault(inst.Type.Fields[i])
		if err != nil {
			return err
		}
		inst.Fields = append(inst.Fields, v)
	}
	return nil
}

// getField implements x.name for everything but the fast path.
func (vm *VM) getField(v Value, name string) (Value, error) {
	switch o := v.O.(type) {
	case *Instance:
		if i, ok := o.Type.fieldIdx[name]; ok {
			if i >= len(o.Fields) {
				if err := vm.migrate(o); err != nil {
					return Nil, err
				}
			}
			return o.Fields[i], nil
		}
		if s, ok := o.Type.statics[name]; ok {
			return s, nil
		}
		m, err := vm.lookup(o.Type, name)
		if err != nil {
			return Nil, err
		}
		if m != nil {
			if m.Static {
				return Nil, errorf("%s is a static method: use %s::%s", name, o.Type.Name, name)
			}
			if m.abstract() {
				return Nil, errorf("method '%s' of %s has no body", name, o.Type.Name)
			}
			return Obj(&BoundMethod{Recv: v, Fn: m.Fn, Name: name}), nil
		}
		return Nil, errorf("%s has no field '%s'", o.Type.Name, name)
	case *EnumValue:
		if i := indexOf(o.Variant.Fields, name); i >= 0 {
			return o.Fields[i], nil
		}
		m, err := vm.lookup(o.Type, name)
		if err != nil {
			return Nil, err
		}
		if m != nil && !m.Static && !m.abstract() {
			return Obj(&BoundMethod{Recv: v, Fn: m.Fn, Name: name}), nil
		}
		return Nil, errorf("%s::%s has no field '%s'", o.Type.Name, o.Variant.Name, name)
	case *TypeObj:
		return vm.scope(v, name)
	case *ModuleObj:
		return Nil, errorf("use %s::%s to get a member of a module", o.Name, name)
	}
	if t := vm.builtinTypeOf(v); t != nil && t.extended {
		if m, _ := vm.lookup(t, name); m != nil && !m.abstract() && !m.Static {
			return Obj(&BoundMethod{Recv: v, Fn: m.Fn, Name: name}), nil
		}
	}
	if hasMethod(v, name) {
		return Obj(&BoundMethod{Recv: v, Name: name}), nil
	}
	return Nil, fieldError(v, name)
}

// setField implements x.name = value.
func (vm *VM) setField(v Value, name string, val Value) error {
	inst, ok := v.O.(*Instance)
	if !ok {
		if e, ok := v.O.(*EnumValue); ok {
			return errorf("cannot change '%s': enum values never change; make a new %s::%s(...)", name, e.Type.Name, e.Variant.Name)
		}
		if t, ok := v.O.(*TypeObj); ok {
			return errorf("cannot change %s::%s: type constants never change", t.Name, name)
		}
		return errorf("cannot set field '%s' of %s", name, TypeName(v))
	}
	i, ok := inst.Type.fieldIdx[name]
	if !ok {
		if _, isStatic := inst.Type.statics[name]; isStatic {
			return errorf("cannot change %s::%s: type constants never change", inst.Type.Name, name)
		}
		return errorf("%s has no field '%s' (fields are declared in the struct)", inst.Type.Name, name)
	}
	if inst.Type.Fields[i].Mode == FieldRequired {
		return errorf("cannot change '%s': it is a const field of %s", name, inst.Type.Name)
	}
	if i >= len(inst.Fields) {
		if err := vm.migrate(inst); err != nil {
			return err
		}
	}
	val, err := vm.checkType(val, inst.Type.Fields[i].Type, "field '"+name+"' of "+inst.Type.Name)
	if err != nil {
		return err
	}
	inst.Fields[i] = val
	return nil
}

// scope implements x::name for types and modules.
func (vm *VM) scope(v Value, name string) (Value, error) {
	switch o := v.O.(type) {
	case *ModuleObj:
		if m, ok := o.Members[name]; ok {
			return m, nil
		}
		if len(name) > 0 && name[0] == '_' {
			return Nil, errorf("'%s' is private to module %s (names starting with _ are not exported)", name, o.Name)
		}
		return Nil, errorf("module %s has no member '%s'", o.Name, name)
	case *TypeObj:
		if o.isEnum {
			if v, ok := o.variantValue(name); ok {
				return v, nil
			}
		}
		if s, ok := o.statics[name]; ok {
			return s, nil
		}
		m, err := vm.lookup(o, name)
		if err != nil {
			return Nil, err
		}
		if m != nil {
			if m.abstract() {
				return Nil, errorf("method '%s' of %s has no body", name, o.Name)
			}
			return m.Fn, nil
		}
		if o.isEnum {
			return Nil, errorf("enum %s has no variant, constant or method named '%s'", o.Name, name)
		}
		return Nil, errorf("%s has no constant or method named '%s'", o.Name, name)
	}
	return Nil, errorf("'::' needs a module or a type on the left, not %s", TypeName(v))
}

// defaultRepr renders an object without a to_str method: point(x: 1, y: 2).
func instanceRepr(b *strings.Builder, o *Instance, field func(Value) error) error {
	b.WriteString(o.Type.Name + "(")
	for i, f := range o.Type.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.Name + ": ")
		v := Nil
		if i < len(o.Fields) {
			v = o.Fields[i]
		}
		if err := field(v); err != nil {
			return err
		}
	}
	b.WriteByte(')')
	return nil
}

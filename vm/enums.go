package vm

import (
	"fmt"
	"strings"
)

// VariantDesc describes one variant of an enum declaration.
type VariantDesc struct {
	Name   string
	Fields []string
	Typed  []bool // which fields have a type on the stack
	Parens bool   // written with (), so it is created by a call even without fields
}

// EnumDesc describes an enum declaration. The stack holds the parent enums,
// then the types of the typed variant fields, in order.
type EnumDesc struct {
	Name     string
	NParents int
	Variants []VariantDesc
}

func (*EnumDesc) TypeName() string { return "desc" }

// VariantInfo is a variant. An enum that inherits a variant shares the same
// VariantInfo, so net_err::denied == io_err::denied.
type VariantInfo struct {
	Name   string
	Fields []string
	Types  []Value // nil entries: any type
	Parens bool
	Origin *TypeObj
}

// EnumValue is a value of an enum: shape::circle(5).
type EnumValue struct {
	Type    *TypeObj // the enum it was made through: its methods apply
	Variant *VariantInfo
	Fields  []Value
}

func (e *EnumValue) TypeName() string { return e.Type.Name }

// VariantCtor creates values of a variant with data: shape::circle is one.
type VariantCtor struct {
	Type    *TypeObj
	Variant *VariantInfo
}

func (*VariantCtor) TypeName() string { return "fn" }

// makeEnum builds an enum type: inherited variants first, then its own.
func (vm *VM) makeEnum(d *EnumDesc, parents, types []Value) (Value, error) {
	t := newType(d.Name)
	t.isEnum = true
	t.variants = map[string]*VariantInfo{}
	t.singletons = map[string]Value{}
	own := map[string]bool{}
	for _, v := range d.Variants {
		own[v.Name] = true
	}
	for _, pv := range parents {
		p, ok := pv.O.(*TypeObj)
		if !ok || !p.isEnum {
			return Nil, errorf("enum %s can only inherit from enums, not %s", d.Name, TypeName(pv))
		}
		t.bases = append(t.bases, p)
		for _, name := range p.variantOrder {
			v := p.variants[name]
			if own[name] {
				continue
			}
			if have, ok := t.variants[name]; ok {
				if have != v {
					return Nil, errorf("enum %s gets variant '%s' from both %s and %s; declare it in %s to choose",
						d.Name, name, have.Origin.Name, v.Origin.Name, d.Name)
				}
				continue
			}
			t.addVariant(v)
		}
	}
	ti := 0
	for _, vd := range d.Variants {
		v := &VariantInfo{Name: vd.Name, Fields: vd.Fields, Parens: vd.Parens, Origin: t}
		v.Types = make([]Value, len(vd.Fields))
		for i := range vd.Fields {
			if i < len(vd.Typed) && vd.Typed[i] {
				v.Types[i] = types[ti]
				ti++
			}
		}
		t.addVariant(v)
	}
	return Obj(t), nil
}

func (t *TypeObj) addVariant(v *VariantInfo) {
	if _, ok := t.variants[v.Name]; !ok {
		t.variantOrder = append(t.variantOrder, v.Name)
	}
	t.variants[v.Name] = v
}

// variantValue implements enum::name: the value itself for a variant
// without (), otherwise its constructor.
func (t *TypeObj) variantValue(name string) (Value, bool) {
	v, ok := t.variants[name]
	if !ok {
		return Nil, false
	}
	if v.Parens || len(v.Fields) > 0 {
		return Obj(&VariantCtor{Type: t, Variant: v}), true
	}
	if s, ok := t.singletons[name]; ok {
		return s, true
	}
	s := Obj(&EnumValue{Type: t, Variant: v})
	t.singletons[name] = s
	return s, true
}

// makeVariant implements shape::circle(5) and shape::rect(w: 2, h: 3).
func (vm *VM) makeVariant(c *VariantCtor, args []Value, kw []Kwarg) (Value, error) {
	v := c.Variant
	full := c.Type.Name + "::" + v.Name
	n := len(v.Fields)
	if len(args) > n {
		return Nil, errorf("%s takes %d value%s but got %d", full, n, plural(n), len(args))
	}
	fields := make([]Value, n)
	set := make([]bool, n)
	for i, a := range args {
		fields[i], set[i] = a, true
	}
	for _, k := range kw {
		i := indexOf(v.Fields, k.Name)
		if i < 0 {
			return Nil, errorf("%s has no field '%s'", full, k.Name)
		}
		if set[i] {
			return Nil, errorf("%s got field '%s' twice", full, k.Name)
		}
		fields[i], set[i] = k.Value, true
	}
	for i, ok := range set {
		if !ok {
			return Nil, errorf("missing field '%s' of %s", v.Fields[i], full)
		}
		f, err := vm.checkType(fields[i], v.Types[i], "field '"+v.Fields[i]+"' of "+full)
		if err != nil {
			return Nil, err
		}
		fields[i] = f
	}
	return Obj(&EnumValue{Type: c.Type, Variant: v, Fields: fields}), nil
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// isVariant reports whether x is a value of the variant that p names (a
// constructor or a value without data). With want >= 0 it also checks that a
// pattern with want sub-patterns fits the variant.
func isVariant(x, p Value, want int) (bool, error) {
	var v *VariantInfo
	var name string
	switch o := p.O.(type) {
	case *VariantCtor:
		v, name = o.Variant, o.Type.Name+"::"+o.Variant.Name
	case *EnumValue:
		v, name = o.Variant, o.Type.Name+"::"+o.Variant.Name
	default:
		return false, errorf("this pattern needs an enum variant, not %s", TypeName(p))
	}
	if want >= 0 && want != len(v.Fields) {
		return false, errorf("the pattern %s(...) has %d part%s, but the variant has %d field%s",
			name, want, plural(want), len(v.Fields), plural(len(v.Fields)))
	}
	e, ok := x.O.(*EnumValue)
	return ok && e.Variant == v, nil
}

// matchEqual is == for match patterns: a variant constructor written without
// parentheses (result::err) matches every value of that variant.
func (vm *VM) matchEqual(x, p Value) (bool, error) {
	if _, ok := p.O.(*VariantCtor); ok {
		return isVariant(x, p, -1)
	}
	return vm.equal(x, p)
}

func variantField(x Value, i int) (Value, error) {
	e, ok := x.O.(*EnumValue)
	if !ok || i >= len(e.Fields) {
		return Nil, errorf("internal error: no field %d in %s", i, TypeName(x))
	}
	return e.Fields[i], nil
}

func enumRepr(b *strings.Builder, e *EnumValue, field func(Value) error) error {
	b.WriteString(e.Type.Name + "::" + e.Variant.Name)
	if !e.Variant.Parens && len(e.Fields) == 0 {
		return nil
	}
	b.WriteByte('(')
	for i, f := range e.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		if err := field(f); err != nil {
			return err
		}
	}
	b.WriteByte(')')
	return nil
}

// enumKey makes a variant without data usable as a map key.
func enumKey(e *EnumValue) (mapKey, error) {
	if len(e.Fields) > 0 {
		return mapKey{}, errorf("%s::%s(...) cannot be a map key: only variants without data can", e.Type.Name, e.Variant.Name)
	}
	return mapKey{k: KObj, s: fmt.Sprintf("\x00variant %p", e.Variant)}, nil
}

// Pushed is how many values OpEnum takes from the stack.
func (d *EnumDesc) Pushed() int {
	n := d.NParents
	for _, v := range d.Variants {
		for _, typed := range v.Typed {
			if typed {
				n++
			}
		}
	}
	return n
}

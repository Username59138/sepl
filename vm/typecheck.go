package vm

// Type annotations are checked while the program runs: a typed variable,
// parameter, result or field accepts nil or a value of its type.
//
//   - builtin types: the value's own type; an int also fits float (and
//     becomes a float)
//   - structs: the struct itself and every struct built on it with "is"
//     or "add struct"
//   - traits: types that took the trait with "is" or "add impl"
//   - enums: values whose variant is a variant of the enum, so a parent
//     enum's value fits where a child enum is expected, not the other way

// fits reports whether v is a value of type t.
func (vm *VM) fits(v Value, t *TypeObj) bool {
	if v.K == KNil {
		return true
	}
	var vt *TypeObj
	switch o := v.O.(type) {
	case *Instance:
		vt = o.Type
	case *EnumValue:
		if t.isEnum {
			return t.variants[o.Variant.Name] == o.Variant
		}
		vt = o.Type
	default:
		vt = vm.typeOf(v)
	}
	if vt == nil {
		return false
	}
	return vt == t || hasBase(vt, t, 0)
}

func hasBase(vt, t *TypeObj, depth int) bool {
	if depth > 64 {
		return false
	}
	for _, b := range vt.bases {
		if b == t || hasBase(b, t, depth+1) {
			return true
		}
	}
	for _, b := range vt.supers {
		if b == t || hasBase(b, t, depth+1) {
			return true
		}
	}
	return false
}

// describeType names the type of a value for error messages.
func describeType(v Value) string {
	if e, ok := v.O.(*EnumValue); ok {
		return e.Type.Name + "::" + e.Variant.Name
	}
	return TypeName(v)
}

// checkType checks v against the type value t; ctx says what is checked
// ("x", "argument 'a' of f()", ...). It returns v, or v as a float when an
// int goes where a float is expected.
func (vm *VM) checkType(v, t Value, ctx string) (Value, error) {
	if t.K == KNil || v.K == KNil {
		return v, nil
	}
	tt, ok := t.O.(*TypeObj)
	if !ok {
		return Nil, errorf("the type of %s is not a type but %s", ctx, TypeName(t))
	}
	if vm.fits(v, tt) {
		return v, nil
	}
	if tt == vm.tFloat && v.K == KInt {
		return Float(float64(v.AsInt())), nil
	}
	return Nil, errorf("%s must be %s, not %s", ctx, tt.Name, describeType(v))
}

// typeOfValue is the type that "let x := v" fixes.
func (vm *VM) typeOfValue(v Value, ctx string) (Value, error) {
	switch o := v.O.(type) {
	case *Instance:
		return Obj(o.Type), nil
	case *EnumValue:
		return Obj(o.Type), nil
	}
	if t := vm.builtinTypeOf(v); t != nil {
		return Obj(t), nil
	}
	if v.K == KNil {
		return Nil, errorf("':=' cannot fix the type of %s from nil; write it: let %s int = nil", ctx, ctx)
	}
	return Nil, errorf("':=' cannot fix the type of %s: %s values have no type name; use '='", ctx, TypeName(v))
}

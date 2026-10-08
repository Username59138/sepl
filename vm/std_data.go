package vm

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- json

func jsonModule(vm *VM) map[string]Value {
	return map[string]Value{
		// parse(text): result::ok(value) or result::err(msg). Objects become
		// maps (keeping key order), arrays lists, numbers int or float.
		"parse": fn("json::parse", 1, 1, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("json::parse", a)
			if err != nil {
				return Nil, err
			}
			dec := json.NewDecoder(strings.NewReader(s[0]))
			dec.UseNumber()
			v, e := decodeJSON(dec)
			if e == nil {
				if _, extra := dec.Token(); extra != io.EOF {
					e = errorf("unexpected data after the JSON value")
				}
			}
			if e != nil {
				return vm.errResult("invalid JSON: " + e.Error()), nil
			}
			return vm.ok(v), nil
		}),
		// to_str(value, indent: 2): JSON text. Structs become objects of their
		// fields, variants without data their name.
		"to_str": fn("json::to_str", 1, 1, []string{"indent"}, func(vm *VM, a []Value, kw map[string]Value) (Value, error) {
			indent := ""
			if n, ok := kw["indent"]; ok {
				if n.K != KInt || n.AsInt() < 0 || n.AsInt() > 16 {
					return Nil, opError("json::to_str() indent must be an int from 0 to 16")
				}
				indent = strings.Repeat(" ", int(n.AsInt()))
			}
			var b strings.Builder
			if err := encodeJSON(&b, a[0], indent, "", 0, vm.fillStale); err != nil {
				return Nil, err
			}
			return Str(b.String()), nil
		}),
	}
}

func decodeJSON(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return Nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := NewMap()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return Nil, err
				}
				v, err := decodeJSON(dec)
				if err != nil {
					return Nil, err
				}
				m.Set(Str(kt.(string)), v)
			}
			_, err := dec.Token()
			return Obj(m), err
		case '[':
			var items []Value
			for dec.More() {
				v, err := decodeJSON(dec)
				if err != nil {
					return Nil, err
				}
				items = append(items, v)
			}
			_, err := dec.Token()
			return NewList(items), err
		}
	case string:
		return Str(t), nil
	case json.Number:
		if i, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return Int(i), nil
		}
		f, err := strconv.ParseFloat(string(t), 64)
		return Float(f), err
	case bool:
		return Bool(t), nil
	case nil:
		return Nil, nil
	}
	return Nil, errorf("unexpected %v", tok)
}

func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// fill completes objects created before add struct gave their type more fields.
func encodeJSON(b *strings.Builder, v Value, indent, prefix string, depth int, fill func(Value) error) error {
	if depth > 200 {
		return opError("json::to_str(): the value is nested too deeply (or contains itself)")
	}
	inner := prefix + indent
	open := func(c byte) {
		b.WriteByte(c)
	}
	sep := func(i int) {
		if i > 0 {
			b.WriteByte(',')
		}
		if indent != "" {
			b.WriteString("\n" + inner)
		}
	}
	closeWith := func(c byte, n int) {
		if indent != "" && n > 0 {
			b.WriteString("\n" + prefix)
		}
		b.WriteByte(c)
	}
	colon := ":"
	if indent != "" {
		colon = ": "
	}
	switch v.K {
	case KNil:
		b.WriteString("null")
		return nil
	case KBool:
		b.WriteString(strconv.FormatBool(v.N != 0))
		return nil
	case KInt:
		b.WriteString(strconv.FormatInt(v.AsInt(), 10))
		return nil
	case KFloat:
		f := v.AsFloat()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return errorf("json::to_str(): %s cannot be written as JSON", FormatFloat(f))
		}
		b.WriteString(FormatFloat(f))
		return nil
	}
	switch o := v.O.(type) {
	case *StrObj:
		b.WriteString(jsonString(o.S))
	case *ListObj:
		open('[')
		for i, x := range o.Items {
			sep(i)
			if err := encodeJSON(b, x, indent, inner, depth+1, fill); err != nil {
				return err
			}
		}
		closeWith(']', len(o.Items))
	case *MapObj:
		open('{')
		for i, k := range o.keys {
			sep(i)
			var key string
			switch {
			case k.K == KObj:
				s, ok := k.AsStr()
				if !ok {
					return errorf("json::to_str(): map keys must be str or numbers, not %s", TypeName(k))
				}
				key = s
			default:
				key = Repr(k)
			}
			b.WriteString(jsonString(key) + colon)
			if err := encodeJSON(b, o.vals[i], indent, inner, depth+1, fill); err != nil {
				return err
			}
		}
		closeWith('}', o.Len())
	case *Instance:
		if err := fill(v); err != nil {
			return err
		}
		open('{')
		for i, f := range o.Type.Fields {
			sep(i)
			b.WriteString(jsonString(f.Name) + colon)
			fv := Nil
			if i < len(o.Fields) {
				fv = o.Fields[i]
			}
			if err := encodeJSON(b, fv, indent, inner, depth+1, fill); err != nil {
				return err
			}
		}
		closeWith('}', len(o.Type.Fields))
	case *EnumValue:
		if len(o.Fields) > 0 {
			return errorf("json::to_str(): %s::%s(...) has data; turn it into a map first", o.Type.Name, o.Variant.Name)
		}
		b.WriteString(jsonString(o.Variant.Name))
	default:
		return errorf("json::to_str(): %s cannot be written as JSON", TypeName(v))
	}
	return nil
}

// ---------------------------------------------------------------- re

// regex compiles a pattern once per VM.
func (vm *VM) regex(fn string, pattern Value) (*regexp.Regexp, error) {
	p, ok := pattern.AsStr()
	if !ok {
		return nil, errorf("%s() needs a str pattern, not %s", fn, TypeName(pattern))
	}
	if re, ok := vm.regexps[p]; ok {
		return re, nil
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, errorf("%s(): bad pattern %s: %s", fn, strconv.Quote(p), strings.TrimPrefix(err.Error(), "error parsing regexp: "))
	}
	if vm.regexps == nil {
		vm.regexps = map[string]*regexp.Regexp{}
	}
	vm.regexps[p] = re
	return re, nil
}

func reFn(name string, nargs int, f func(vm *VM, re *regexp.Regexp, s []string) (Value, error)) Value {
	return fn("re::"+name, nargs, nargs, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
		re, err := vm.regex("re::"+name, a[0])
		if err != nil {
			return Nil, err
		}
		s, err := strArgs("re::"+name, a[1:])
		if err != nil {
			return Nil, err
		}
		return f(vm, re, s)
	})
}

func strList(items []string) Value {
	out := make([]Value, len(items))
	for i, s := range items {
		out[i] = Str(s)
	}
	return NewList(out)
}

func reModule(vm *VM) map[string]Value {
	return map[string]Value{
		// matches(pattern, text): does the pattern occur in the text?
		// Use ^...$ to match the whole text.
		"matches": reFn("matches", 2, func(vm *VM, re *regexp.Regexp, s []string) (Value, error) {
			return Bool(re.MatchString(s[0])), nil
		}),
		// find(pattern, text): option::some(first match) or option::none
		"find": reFn("find", 2, func(vm *VM, re *regexp.Regexp, s []string) (Value, error) {
			if m := re.FindStringIndex(s[0]); m != nil {
				return vm.some(Str(s[0][m[0]:m[1]])), nil
			}
			return vm.none(), nil
		}),
		"find_all": reFn("find_all", 2, func(vm *VM, re *regexp.Regexp, s []string) (Value, error) {
			return strList(re.FindAllString(s[0], -1)), nil
		}),
		// groups(pattern, text): option::some([whole match, group 1, ...])
		"groups": reFn("groups", 2, func(vm *VM, re *regexp.Regexp, s []string) (Value, error) {
			if m := re.FindStringSubmatch(s[0]); m != nil {
				return vm.some(strList(m)), nil
			}
			return vm.none(), nil
		}),
		// replace(pattern, text, with): $1 in with is group 1
		"replace": reFn("replace", 3, func(vm *VM, re *regexp.Regexp, s []string) (Value, error) {
			return Str(re.ReplaceAllString(s[0], s[1])), nil
		}),
		"split": reFn("split", 2, func(vm *VM, re *regexp.Regexp, s []string) (Value, error) {
			return strList(re.Split(s[0], -1)), nil
		}),
	}
}

// ---------------------------------------------------------------- time format

// formatTime implements a small strftime: %Y %m %d %H %M %S %%.
func formatTime(t time.Time, layout string) string {
	var b strings.Builder
	for i := 0; i < len(layout); i++ {
		c := layout[i]
		if c != '%' || i+1 == len(layout) {
			b.WriteByte(c)
			continue
		}
		i++
		switch layout[i] {
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'm':
			b.WriteString(pad2(int(t.Month())))
		case 'd':
			b.WriteString(pad2(t.Day()))
		case 'H':
			b.WriteString(pad2(t.Hour()))
		case 'M':
			b.WriteString(pad2(t.Minute()))
		case 'S':
			b.WriteString(pad2(t.Second()))
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(layout[i])
		}
	}
	return b.String()
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

package vm

import "math"

// MapObj is a map that remembers insertion order.
type MapObj struct {
	index map[mapKey]int
	keys  []Value
	vals  []Value
}

func (*MapObj) TypeName() string { return "map" }

// mapKey is the hashable form of a key. 1 and 1.0 are the same key.
type mapKey struct {
	k Kind
	n uint64
	s string
}

func NewMap() *MapObj { return &MapObj{index: map[mapKey]int{}} }

func keyOf(v Value) (mapKey, error) {
	switch v.K {
	case KNil, KBool, KInt:
		return mapKey{k: v.K, n: v.N}, nil
	case KFloat:
		f := v.AsFloat()
		if f == math.Trunc(f) && f >= math.MinInt64 && f < math.MaxInt64 {
			return mapKey{k: KInt, n: uint64(int64(f))}, nil
		}
		return mapKey{k: KFloat, n: v.N}, nil
	}
	if s, ok := v.O.(*StrObj); ok {
		return mapKey{k: KObj, s: s.S}, nil
	}
	if e, ok := v.O.(*EnumValue); ok {
		return enumKey(e)
	}
	return mapKey{}, errorf("%s cannot be a map key (use nil, bool, int, float or str)", TypeName(v))
}

func (m *MapObj) Len() int { return len(m.keys) }

// Keys returns the keys in insertion order (do not modify).
func (m *MapObj) Keys() []Value { return m.keys }

// Values returns the values in insertion order (do not modify).
func (m *MapObj) Values() []Value { return m.vals }

func (m *MapObj) Get(k Value) (Value, bool) {
	key, err := keyOf(k)
	if err != nil {
		return Nil, false
	}
	if i, ok := m.index[key]; ok {
		return m.vals[i], true
	}
	return Nil, false
}

func (m *MapObj) Set(k, v Value) error {
	key, err := keyOf(k)
	if err != nil {
		return err
	}
	if i, ok := m.index[key]; ok {
		m.vals[i] = v
		return nil
	}
	m.index[key] = len(m.keys)
	m.keys = append(m.keys, k)
	m.vals = append(m.vals, v)
	return nil
}

// Delete removes k and returns its value.
func (m *MapObj) Delete(k Value) (Value, bool) {
	key, err := keyOf(k)
	if err != nil {
		return Nil, false
	}
	i, ok := m.index[key]
	if !ok {
		return Nil, false
	}
	v := m.vals[i]
	delete(m.index, key)
	m.keys = append(m.keys[:i], m.keys[i+1:]...)
	m.vals = append(m.vals[:i], m.vals[i+1:]...)
	for j := i; j < len(m.keys); j++ {
		kk, _ := keyOf(m.keys[j])
		m.index[kk] = j
	}
	return v, true
}

func (m *MapObj) Clear() {
	m.index = map[mapKey]int{}
	m.keys, m.vals = nil, nil
}

func (m *MapObj) Copy() *MapObj {
	c := NewMap()
	for i, k := range m.keys {
		c.Set(k, m.vals[i])
	}
	return c
}

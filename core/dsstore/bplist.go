package dsstore

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"
)

// Minimal binary-plist (bplist00) writer for the flat dicts in bwsp/icvp.
// Supported value types: bool, int (int64), float (float64), string, []byte, and
// map[string]any (one level of nesting is enough here).

type bplObj struct {
	kind byte // 'b','i','f','s','d','m'
	b    bool
	i    int64
	f    float64
	s    string
	data []byte
	m    map[string]any
}

func encodeBplist(root map[string]any) []byte {
	var objs []bplObj
	add := func(o bplObj) int { objs = append(objs, o); return len(objs) - 1 }
	var flatten func(v any) int
	flatten = func(v any) int {
		switch x := v.(type) {
		case bool:
			return add(bplObj{kind: 'b', b: x})
		case int:
			return add(bplObj{kind: 'i', i: int64(x)})
		case int64:
			return add(bplObj{kind: 'i', i: x})
		case float64:
			return add(bplObj{kind: 'f', f: x})
		case string:
			return add(bplObj{kind: 's', s: x})
		case []byte:
			return add(bplObj{kind: 'd', data: x})
		case map[string]any:
			idx := add(bplObj{kind: 'm', m: x})
			return idx
		}
		panic("bplist: unsupported type")
	}
	rootIdx := flatten(root)
	// Resolve child refs for dicts (stable key order for determinism).
	dictKeys := map[int][]string{}
	dictKeyRef := map[int][]int{}
	dictValRef := map[int][]int{}
	var resolve func(idx int)
	resolve = func(idx int) {
		o := objs[idx]
		if o.kind != 'm' {
			return
		}
		keys := make([]string, 0, len(o.m))
		for k := range o.m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		dictKeys[idx] = keys
		for _, k := range keys {
			kr := add(bplObj{kind: 's', s: k})
			vr := flatten(o.m[k])
			dictKeyRef[idx] = append(dictKeyRef[idx], kr)
			dictValRef[idx] = append(dictValRef[idx], vr)
			resolve(vr)
		}
	}
	resolve(rootIdx)

	refSize := 1
	if len(objs) >= 256 {
		refSize = 2
	}
	writeRef := func(b *bytes.Buffer, r int) {
		if refSize == 1 {
			b.WriteByte(byte(r))
		} else {
			binary.Write(b, binary.BigEndian, uint16(r))
		}
	}
	writeLen := func(b *bytes.Buffer, marker byte, n int) {
		if n < 15 {
			b.WriteByte(marker | byte(n))
		} else {
			b.WriteByte(marker | 0x0f)
			writeIntObj(b, int64(n))
		}
	}

	out := new(bytes.Buffer)
	out.WriteString("bplist00")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = out.Len()
		switch o.kind {
		case 'b':
			if o.b {
				out.WriteByte(0x09)
			} else {
				out.WriteByte(0x08)
			}
		case 'i':
			writeIntObj(out, o.i)
		case 'f':
			out.WriteByte(0x23)
			binary.Write(out, binary.BigEndian, math.Float64bits(o.f))
		case 's':
			writeLen(out, 0x50, len(o.s)) // ASCII string
			out.WriteString(o.s)
		case 'd':
			writeLen(out, 0x40, len(o.data))
			out.Write(o.data)
		case 'm':
			keys := dictKeys[i]
			writeLen(out, 0xD0, len(keys))
			for _, kr := range dictKeyRef[i] {
				writeRef(out, kr)
			}
			for _, vr := range dictValRef[i] {
				writeRef(out, vr)
			}
		}
	}
	offTableOff := out.Len()
	offSize := 1
	if offTableOff >= 256 {
		offSize = 2
	}
	if offTableOff >= 65536 {
		offSize = 4
	}
	for _, off := range offsets {
		switch offSize {
		case 1:
			out.WriteByte(byte(off))
		case 2:
			binary.Write(out, binary.BigEndian, uint16(off))
		default:
			binary.Write(out, binary.BigEndian, uint32(off))
		}
	}
	// trailer
	out.Write(make([]byte, 5))
	out.WriteByte(0) // sortVersion
	out.WriteByte(byte(offSize))
	out.WriteByte(byte(refSize))
	binary.Write(out, binary.BigEndian, uint64(len(objs)))
	binary.Write(out, binary.BigEndian, uint64(rootIdx))
	binary.Write(out, binary.BigEndian, uint64(offTableOff))
	return out.Bytes()
}

func writeIntObj(b *bytes.Buffer, v int64) {
	switch {
	case v >= 0 && v < 256:
		b.WriteByte(0x10)
		b.WriteByte(byte(v))
	case v >= 0 && v < 65536:
		b.WriteByte(0x11)
		binary.Write(b, binary.BigEndian, uint16(v))
	case v >= 0 && v < 1<<32:
		b.WriteByte(0x12)
		binary.Write(b, binary.BigEndian, uint32(v))
	default:
		b.WriteByte(0x13)
		binary.Write(b, binary.BigEndian, uint64(v))
	}
}

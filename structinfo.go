package asun

import (
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Struct field metadata, shared by the text and binary codecs
// ---------------------------------------------------------------------------

type fieldInfo struct {
	name      string
	index     []int
	direct    int          // index into the struct when not promoted from an embedded struct, else -1
	fieldType reflect.Type // field type
}

type structInfo struct {
	fields     []fieldInfo
	structType reflect.Type
	nameIndex  map[string]int // wire name → index in fields
	// static: every field is a scalar or a static struct, so the text schema
	// cannot depend on the values and is cached in headers[typed].
	static  bool
	headers [2]atomic.Pointer[[]byte]
}

var structCache sync.Map // map[reflect.Type]*structInfo

// lastStruct short-cuts the common case of one struct type used repeatedly.
var lastStruct atomic.Pointer[structInfo]

func getStructInfo(t reflect.Type) *structInfo {
	if si := lastStruct.Load(); si != nil && si.structType == t {
		return si
	}
	var si *structInfo
	if v, ok := structCache.Load(t); ok {
		si = v.(*structInfo)
	} else {
		actual, _ := structCache.LoadOrStore(t, buildStructInfo(t))
		si = actual.(*structInfo)
	}
	lastStruct.Store(si)
	return si
}

// field returns field i of the struct value rv.
func (si *structInfo) field(rv reflect.Value, i int) reflect.Value {
	fi := &si.fields[i]
	if fi.direct >= 0 {
		return rv.Field(fi.direct)
	}
	return rv.FieldByIndex(fi.index)
}

func buildStructInfo(t reflect.Type) *structInfo {
	n := t.NumField()
	fields := make([]fieldInfo, 0, n)
	for i := 0; i < n; i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		direct := -1
		if len(f.Index) == 1 {
			direct = f.Index[0]
		}
		fi := fieldInfo{index: f.Index, direct: direct, fieldType: f.Type}

		// Check asun tag first, then json tag
		if tag, ok := f.Tag.Lookup("asun"); ok {
			if tag == "-" {
				continue
			}
			name := tag
			if idx := indexOf(name, ','); idx >= 0 {
				name = name[:idx]
			}
			fi.name = name
		}
		if fi.name == "" {
			if tag, ok := f.Tag.Lookup("json"); ok {
				if tag == "-" {
					continue
				}
				name := tag
				if idx := indexOf(name, ','); idx >= 0 {
					name = name[:idx]
				}
				fi.name = name
			}
		}
		if fi.name == "" {
			fi.name = f.Name
		}

		// Embedded structs contribute their fields directly.
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			embedded := getStructInfo(f.Type)
			for _, ef := range embedded.fields {
				ef2 := ef
				ef2.index = append(append([]int{}, f.Index...), ef.index...)
				ef2.direct = -1
				fields = append(fields, ef2)
			}
			continue
		}

		fields = append(fields, fi)
	}
	si := &structInfo{fields: fields, structType: t, static: true}
	for _, fi := range fields {
		if !isStaticType(fi.fieldType) {
			si.static = false
		}
	}
	si.nameIndex = make(map[string]int, len(fields))
	for i, fi := range fields {
		si.nameIndex[fi.name] = i
	}
	return si
}

// isStaticType reports whether the text schema of t is fixed by the type.
// Pointers (null drops the hint), slices (empty ones have no element hint),
// arrays and interfaces are value-dependent.
func isStaticType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	case reflect.Struct:
		return getStructInfo(t).static
	}
	return false
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// unsafeString converts a byte slice to string without copying.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// ---------------------------------------------------------------------------
// Map fields
// ---------------------------------------------------------------------------

var errMapFieldsUnsupported = &MarshalError{
	Message: "map fields are not supported",
}

var noMapTypeCache sync.Map // map[reflect.Type]bool

func validateNoMapType(t reflect.Type, seen map[reflect.Type]bool) error {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if seen[t] {
		return nil
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Map:
		return errMapFieldsUnsupported
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			if err := validateNoMapType(f.Type, seen); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		return validateNoMapType(t.Elem(), seen)
	}
	return nil
}

func ensureNoMapType(t reflect.Type) error {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if cached, ok := noMapTypeCache.Load(t); ok {
		if cached.(bool) {
			return nil
		}
		return errMapFieldsUnsupported
	}
	err := validateNoMapType(t, make(map[reflect.Type]bool))
	noMapTypeCache.Store(t, err == nil)
	return err
}

// ---------------------------------------------------------------------------
// Bounded cache
// ---------------------------------------------------------------------------

// boundedCache is a concurrency-safe cache with a hard entry cap. Without a
// cap, every distinct schema in (untrusted) input would be retained forever.
// When the cap is reached the whole map is dropped and repopulated:
// legitimate workloads use a small, stable set of schemas and never hit it.
type boundedCache struct {
	m   sync.Map
	n   atomic.Int64
	max int64
}

func (c *boundedCache) Load(key string) (any, bool) {
	return c.m.Load(key)
}

// LoadOrStore returns the existing value for key if present; otherwise stores
// and returns value. It flushes the cache before inserting if the cap is hit.
func (c *boundedCache) LoadOrStore(key string, value any) (any, bool) {
	if actual, ok := c.m.Load(key); ok {
		return actual, true
	}
	if c.n.Load() >= c.max {
		c.m.Range(func(k, _ any) bool {
			// Concurrent resets may race on a key: count only real removals.
			if _, ok := c.m.LoadAndDelete(k); ok {
				c.n.Add(-1)
			}
			return true
		})
	}
	actual, loaded := c.m.LoadOrStore(key, value)
	if !loaded {
		c.n.Add(1)
	}
	return actual, loaded
}

// maxCachedSchemas caps distinct schema headers retained by the decoder.
const maxCachedSchemas = 4096

var schemaCache = boundedCache{max: maxCachedSchemas}

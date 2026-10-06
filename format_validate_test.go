package asun

// Port of asun-rs tests/format_validate_test.rs: document shape validation
// and pretty-print round trips.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type fvUser struct {
	ID   int64  `asun:"id"`
	Name string `asun:"name"`
}

type fvScore struct {
	ID    int64   `asun:"id"`
	Value float64 `asun:"value"`
	Label string  `asun:"label"`
}

type fvInner struct {
	X     int64  `asun:"x"`
	Label string `asun:"label"`
}

type fvOuter struct {
	ID    int64   `asun:"id"`
	Inner fvInner `asun:"inner"`
}

const (
	fvBadFmt  = "{id@int, name@str}:\n  (1, Alice),\n  (2, Bob),\n  (3, Carol)"
	fvGoodFmt = "[{id@int, name@str}]:\n  (1, Alice),\n  (2, Bob),\n  (3, Carol)"
)

func TestFormatValidateRejectsBadShapes(t *testing.T) {
	for _, c := range []struct {
		name, in string
		target   any
	}{
		{"{schema}: rows as slice", fvBadFmt, &[]fvUser{}},
		{"{schema}: trailing rows as struct", fvBadFmt, &fvUser{}},
		{"extra tuple after struct", "{id@int,name@str}:(10,Dave),(11,Eve)", &fvUser{}},
		{"many rows without []", "{id,name}:(1,A),(2,B),(3,C),(4,D),(5,E)", &[]fvUser{}},
	} {
		if err := Decode([]byte(c.in), c.target); err == nil {
			t.Errorf("%s: accepted %q", c.name, c.in)
		}
	}
}

func TestFormatValidateAcceptsGoodShapes(t *testing.T) {
	var users []fvUser
	if err := Decode([]byte(fvGoodFmt), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 || users[0].Name != "Alice" || users[2].Name != "Carol" {
		t.Fatalf("%+v", users)
	}
	users = nil
	if err := Decode([]byte("[{id,name}]:\n  (1,Alice),\n  (2,Bob),\n  (3,Carol)"), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 || users[1].Name != "Bob" {
		t.Fatalf("%+v", users)
	}
	var u fvUser
	if err := Decode([]byte("{id@int, name@str}:(1,Alice)"), &u); err != nil || u != (fvUser{1, "Alice"}) {
		t.Fatalf("%+v %v", u, err)
	}
	users = nil
	if err := Decode([]byte("[{id@int, name@str}]:(1,Alice)"), &users); err != nil || len(users) != 1 || users[0] != (fvUser{1, "Alice"}) {
		t.Fatalf("%+v %v", users, err)
	}
}

// prettyRoundtrip encodes v with enc and decodes it back into a new value of
// the same type.
func prettyRoundtrip(t *testing.T, v any, enc func(any) ([]byte, error)) string {
	t.Helper()
	pretty, err := enc(v)
	if err != nil {
		t.Fatal(err)
	}
	back := reflect.New(reflect.TypeOf(v))
	if err := Decode(pretty, back.Interface()); err != nil {
		t.Fatalf("decode %q: %v", pretty, err)
	}
	if !reflect.DeepEqual(back.Elem().Interface(), v) {
		t.Fatalf("roundtrip mismatch for %q: %+v", pretty, back.Elem().Interface())
	}
	return string(pretty)
}

func TestFormatValidatePrettyRoundtrips(t *testing.T) {
	prettyRoundtrip(t, fvUser{42, "Alice"}, EncodePretty)
	if p := prettyRoundtrip(t, fvUser{7, "Bob"}, EncodePrettyTyped); !strings.Contains(p, "@int") {
		t.Fatalf("no hints: %s", p)
	}
	users := []fvUser{{1, "Alice"}, {2, "Bob"}, {3, "Carol"}}
	if p := prettyRoundtrip(t, users, EncodePretty); !strings.Contains(p, "\n") {
		t.Fatalf("expected multi-line output: %s", p)
	}
	prettyRoundtrip(t, []fvScore{{1, 95.5, "excellent"}, {2, 72.3, "good"}, {3, 40.0, "fail"}}, EncodePretty)
	prettyRoundtrip(t, fvOuter{5, fvInner{10, "test"}}, EncodePretty)
	many := make([]fvUser, 50)
	for i := range many {
		many[i] = fvUser{int64(i + 1), fmt.Sprintf("user%d", i+1)}
	}
	prettyRoundtrip(t, many, EncodePretty)
	if p := prettyRoundtrip(t, users[:2], EncodePrettyTyped); !strings.Contains(p, "@int") {
		t.Fatalf("no hints: %s", p)
	}
}

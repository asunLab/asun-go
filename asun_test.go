package asun

import (
	"bytes"
	"reflect"
	"testing"
)

type User struct {
	ID     int64  `asun:"id"`
	Name   string `asun:"name"`
	Active bool   `asun:"active"`
}

type Score struct {
	ID    int64   `asun:"id"`
	Value float64 `asun:"value"`
}

type Item struct {
	ID    int64   `asun:"id"`
	Label *string `asun:"label"`
}

type Tagged struct {
	Name string   `asun:"name"`
	Tags []string `asun:"tags"`
}

type Note struct {
	Text string `asun:"text"`
}

type Row struct {
	ID   int64  `asun:"id"`
	Name string `asun:"name"`
}

func TestSerializeStruct(t *testing.T) {
	u := User{ID: 1, Name: "Alice", Active: true}
	got, err := Encode(u)
	if err != nil {
		t.Fatal(err)
	}
	expect := "{id,name,active}:(1,Alice,true)"
	if string(got) != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
}

func TestDeserializeStructWithSchema(t *testing.T) {
	input := "{id,name,active}:(1,Alice,true)"
	var u User
	if err := Decode([]byte(input), &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 || u.Name != "Alice" || !u.Active {
		t.Fatalf("unexpected: %+v", u)
	}
}

func TestDeserializeStructWithTypedSchema(t *testing.T) {
	input := "{id@int,name@str,active@bool}:(1,Alice,true)"
	var u User
	if err := Decode([]byte(input), &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 || u.Name != "Alice" || !u.Active {
		t.Fatalf("unexpected: %+v", u)
	}
}

func TestRoundtrip(t *testing.T) {
	u := User{ID: 42, Name: "Bob", Active: false}
	data, err := Encode(u)
	if err != nil {
		t.Fatal(err)
	}
	var u2 User
	if err := Decode(data, &u2); err != nil {
		t.Fatal(err)
	}
	if u != u2 {
		t.Fatalf("roundtrip failed: %+v != %+v", u, u2)
	}
}

func TestVecDeserialize(t *testing.T) {
	input := "[{id,name,active}]:(1,Alice,true),(2,Bob,false)"
	var users []User
	if err := Decode([]byte(input), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2, got %d", len(users))
	}
	if users[0].Name != "Alice" || users[1].Name != "Bob" {
		t.Fatalf("unexpected: %+v", users)
	}
}

func TestMultiline(t *testing.T) {
	input := "[{id,name,active}]:\n  (1, Alice, true),\n  (2, Bob, false)"
	var users []User
	if err := Decode([]byte(input), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2, got %d", len(users))
	}
}

func TestQuotedString(t *testing.T) {
	input := `{id,name,active}:(1,"Carol Smith",true)`
	var u User
	if err := Decode([]byte(input), &u); err != nil {
		t.Fatal(err)
	}
	if u.Name != "Carol Smith" {
		t.Fatalf("got %q", u.Name)
	}
}

func TestOptionalField(t *testing.T) {
	input := "{id,label}:(1,)"
	var item Item
	if err := Decode([]byte(input), &item); err != nil {
		t.Fatal(err)
	}
	if item.ID != 1 || item.Label != nil {
		t.Fatalf("unexpected: %+v", item)
	}
}

func TestArrayField(t *testing.T) {
	input := "{name,tags}:(Alice,[rust,go])"
	var tg Tagged
	if err := Decode([]byte(input), &tg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tg.Tags, []string{"rust", "go"}) {
		t.Fatalf("unexpected tags: %v", tg.Tags)
	}
}

func TestNestedStruct(t *testing.T) {
	type Dept struct {
		Title string `asun:"title"`
	}
	type Employee struct {
		Name string `asun:"name"`
		Dept Dept   `asun:"dept"`
	}
	input := "{name,dept@{title}}:(Alice,(Manager))"
	var e Employee
	if err := Decode([]byte(input), &e); err != nil {
		t.Fatal(err)
	}
	if e.Name != "Alice" || e.Dept.Title != "Manager" {
		t.Fatalf("unexpected: %+v", e)
	}
}

func TestSerializeVec(t *testing.T) {
	rows := []Row{{ID: 1, Name: "Alice"}, {ID: 2, Name: "Bob"}}
	got, err := Encode(rows)
	if err != nil {
		t.Fatal(err)
	}
	expect := "[{id,name}]:(1,Alice),(2,Bob)"
	if string(got) != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
}

func TestEscapeRoundtrip(t *testing.T) {
	note := Note{Text: "hello, world (test)"}
	data, err := Encode(note)
	if err != nil {
		t.Fatal(err)
	}
	var note2 Note
	if err := Decode(data, &note2); err != nil {
		t.Fatal(err)
	}
	if note != note2 {
		t.Fatalf("roundtrip failed: %+v != %+v", note, note2)
	}
}

func TestAtStringRoundtripAllApis(t *testing.T) {
	note := Note{Text: "@Alice"}

	untyped, err := Encode(note)
	if err != nil {
		t.Fatal(err)
	}
	if string(untyped) != `{text}:(@Alice)` {
		t.Fatalf("unexpected untyped encode: %s", untyped)
	}
	var out Note
	if err := Decode(untyped, &out); err != nil {
		t.Fatal(err)
	}
	if out != note {
		t.Fatalf("Decode mismatch: %+v != %+v", out, note)
	}

	typed, err := EncodeTyped(note)
	if err != nil {
		t.Fatal(err)
	}
	if string(typed) != `{text@str}:(@Alice)` {
		t.Fatalf("unexpected typed encode: %s", typed)
	}
	out = Note{}
	if err := Decode(typed, &out); err != nil {
		t.Fatal(err)
	}
	if out != note {
		t.Fatalf("typed Decode mismatch: %+v != %+v", out, note)
	}

	pretty, err := EncodePretty(note)
	if err != nil {
		t.Fatal(err)
	}
	out = Note{}
	if err := Decode(pretty, &out); err != nil {
		t.Fatal(err)
	}
	if out != note {
		t.Fatalf("pretty Decode mismatch: %+v != %+v", out, note)
	}

	prettyTyped, err := EncodePrettyTyped(note)
	if err != nil {
		t.Fatal(err)
	}
	out = Note{}
	if err := Decode(prettyTyped, &out); err != nil {
		t.Fatal(err)
	}
	if out != note {
		t.Fatalf("pretty typed Decode mismatch: %+v != %+v", out, note)
	}

	bin, err := EncodeBinary(note)
	if err != nil {
		t.Fatal(err)
	}
	out = Note{}
	if err := DecodeBinary(bin, &out); err != nil {
		t.Fatal(err)
	}
	if out != note {
		t.Fatalf("binary Decode mismatch: %+v != %+v", out, note)
	}
}

// ASUN 1.5 forbids a trailing comma after the last row (as asun-rs does).
func TestTrailingCommaRejected(t *testing.T) {
	input := "[{id,name,active}]:(1,Alice,true),(2,Bob,false),"
	var users []User
	if err := Decode([]byte(input), &users); err == nil {
		t.Fatalf("expected an error for a trailing comma, got %+v", users)
	}
}

func TestCommentStripping(t *testing.T) {
	input := "/* users */ {id,name,active}:(1,Alice,true)"
	var u User
	if err := Decode([]byte(input), &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 {
		t.Fatalf("unexpected id: %d", u.ID)
	}
}

func TestFloatField(t *testing.T) {
	input := "{id,value}:(1,95.5)"
	var s Score
	if err := Decode([]byte(input), &s); err != nil {
		t.Fatal(err)
	}
	if s.Value != 95.5 {
		t.Fatalf("got %f", s.Value)
	}
}

func TestEntryListField(t *testing.T) {
	type AttrEntry struct {
		Key   string `asun:"key"`
		Value int64  `asun:"value"`
	}
	type EntryItem struct {
		Name  string      `asun:"name"`
		Attrs []AttrEntry `asun:"attrs"`
	}
	input := "{name,attrs@[{key,value}]}:(Alice,[(age,30),(score,95)])"
	var item EntryItem
	if err := Decode([]byte(input), &item); err != nil {
		t.Fatal(err)
	}
	if len(item.Attrs) != 2 || item.Attrs[0].Key != "age" || item.Attrs[0].Value != 30 || item.Attrs[1].Key != "score" || item.Attrs[1].Value != 95 {
		t.Fatalf("unexpected: %+v", item.Attrs)
	}
}

func TestAnnotatedSimpleStruct(t *testing.T) {
	typed := "{id@int,name@str,active@bool}:(42,Bob,false)"
	untyped := "{id,name,active}:(42,Bob,false)"
	var u1, u2 User
	if err := Decode([]byte(typed), &u1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untyped), &u2); err != nil {
		t.Fatal(err)
	}
	if u1 != u2 {
		t.Fatalf("mismatch")
	}
	if u1.ID != 42 || u1.Name != "Bob" || u1.Active {
		t.Fatalf("unexpected: %+v", u1)
	}
}

func TestAnnotatedVec(t *testing.T) {
	typed := "[{id@int,name@str,active@bool}]:(1,Alice,true),(2,Bob,false)"
	untyped := "[{id,name,active}]:(1,Alice,true),(2,Bob,false)"
	var v1, v2 []User
	if err := Decode([]byte(typed), &v1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untyped), &v2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v1, v2) || len(v1) != 2 {
		t.Fatalf("mismatch")
	}
}

func TestAnnotatedNestedStruct(t *testing.T) {
	type Dept struct {
		Title  string  `asun:"title"`
		Budget float64 `asun:"budget"`
	}
	type Employee struct {
		Name   string `asun:"name"`
		Age    int64  `asun:"age"`
		Dept   Dept   `asun:"dept"`
		Active bool   `asun:"active"`
	}
	typed := "{name@str,age@int,dept@{title@str,budget@float},active@bool}:(Alice,30,(Engineering,50000.5),true)"
	untyped := "{name,age,dept@{title,budget},active}:(Alice,30,(Engineering,50000.5),true)"
	var e1, e2 Employee
	if err := Decode([]byte(typed), &e1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untyped), &e2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e1, e2) {
		t.Fatalf("mismatch")
	}
	if e1.Name != "Alice" || e1.Dept.Title != "Engineering" || e1.Dept.Budget != 50000.5 {
		t.Fatalf("unexpected: %+v", e1)
	}
}

func TestAnnotatedWithArrays(t *testing.T) {
	type Profile struct {
		Name   string   `asun:"name"`
		Scores []int64  `asun:"scores"`
		Tags   []string `asun:"tags"`
	}
	typed := "{name@str,scores@[int],tags@[str]}:(Alice,[90,85,92],[rust,go])"
	untyped := "{name,scores,tags}:(Alice,[90,85,92],[rust,go])"
	var p1, p2 Profile
	if err := Decode([]byte(typed), &p1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untyped), &p2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p1, p2) {
		t.Fatalf("mismatch")
	}
	if !reflect.DeepEqual(p1.Scores, []int64{90, 85, 92}) {
		t.Fatalf("unexpected scores: %v", p1.Scores)
	}
}

func TestAnnotatedWithEntryList(t *testing.T) {
	type AttrEntry struct {
		Key   string `asun:"key"`
		Value int64  `asun:"value"`
	}
	type Config struct {
		Name  string      `asun:"name"`
		Attrs []AttrEntry `asun:"attrs"`
	}
	typed := "{name@str,attrs@[{key@str,value@int}]}:(server,[(port,8080),(timeout,30)])"
	untyped := "{name,attrs@[{key,value}]}:(server,[(port,8080),(timeout,30)])"
	var c1, c2 Config
	if err := Decode([]byte(typed), &c1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untyped), &c2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c1, c2) {
		t.Fatalf("mismatch")
	}
	if len(c1.Attrs) != 2 || c1.Attrs[0].Key != "port" || c1.Attrs[0].Value != 8080 {
		t.Fatalf("unexpected attrs: %+v", c1.Attrs)
	}
}

func TestComplexEntryListRoundtrip(t *testing.T) {
	type Person struct {
		Name string `asun:"name"`
		Age  int64  `asun:"age"`
	}
	type GroupEntry struct {
		Key   string   `asun:"key"`
		Value []Person `asun:"value"`
	}
	type Groups struct {
		Groups []GroupEntry `asun:"groups"`
	}
	src := Groups{
		Groups: []GroupEntry{
			{Key: "teamA", Value: []Person{{Name: "Alice", Age: 30}, {Name: "Bob", Age: 28}}},
			{Key: "teamB", Value: []Person{{Name: "Carol", Age: 41}}},
		},
	}
	data, err := Encode(&src)
	if err != nil {
		t.Fatal(err)
	}
	var out Groups
	if err := Decode(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(src, out) {
		t.Fatalf("mismatch: %#v vs %#v", src, out)
	}
}

func TestAnnotatedWithComplexEntryList(t *testing.T) {
	type Person struct {
		Name string `asun:"name"`
		Age  int64  `asun:"age"`
	}
	type GroupEntry struct {
		Key   string   `asun:"key"`
		Value []Person `asun:"value"`
	}
	type Groups struct {
		Groups []GroupEntry `asun:"groups"`
	}
	typed := "{groups@[{key@str,value@[{name@str,age@int}]}]}:([(teamA,[(Alice,30),(Bob,28)]),(teamB,[(Carol,41)])])"
	untyped := "{groups@[{key,value@[{name,age}]}]}:([(teamA,[(Alice,30),(Bob,28)]),(teamB,[(Carol,41)])])"
	var g1, g2 Groups
	if err := Decode([]byte(typed), &g1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untyped), &g2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g1, g2) {
		t.Fatalf("mismatch: %#v vs %#v", g1, g2)
	}
	if len(g1.Groups) != 2 || g1.Groups[0].Key != "teamA" || len(g1.Groups[0].Value) != 2 || g1.Groups[1].Value[0].Age != 41 {
		t.Fatalf("unexpected: %#v", g1)
	}
}

func TestEncodeTypedComplexEntryListSchema(t *testing.T) {
	type Person struct {
		Name string `asun:"name"`
		Age  int64  `asun:"age"`
	}
	type GroupEntry struct {
		Key   string   `asun:"key"`
		Value []Person `asun:"value"`
	}
	type Groups struct {
		Groups []GroupEntry `asun:"groups"`
	}
	src := Groups{
		Groups: []GroupEntry{
			{Key: "teamA", Value: []Person{{Name: "Alice", Age: 30}}},
		},
	}
	data, err := EncodeTyped(&src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("groups@[{key@str,value@[{name@str,age@int}]}]")) {
		t.Fatalf("missing complex entry-list schema: %s", data)
	}
}

func TestPrettyTypedComplexEntryListRoundtrip(t *testing.T) {
	type Person struct {
		Name string `asun:"name"`
		Age  int64  `asun:"age"`
	}
	type GroupEntry struct {
		Key   string   `asun:"key"`
		Value []Person `asun:"value"`
	}
	type Groups struct {
		Groups []GroupEntry `asun:"groups"`
	}
	src := Groups{
		Groups: []GroupEntry{
			{Key: "teamA", Value: []Person{{Name: "Alice", Age: 30}, {Name: "Bob", Age: 28}}},
			{Key: "teamB", Value: []Person{{Name: "Carol", Age: 41}}},
		},
	}
	data, err := EncodePrettyTyped(&src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("groups@[{")) {
		t.Fatalf("missing complex entry-list typed schema: %s", data)
	}
	var out Groups
	if err := Decode(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(src, out) {
		t.Fatalf("mismatch: %#v vs %#v", src, out)
	}
}

func TestBinaryComplexEntryListRoundtrip(t *testing.T) {
	type Person struct {
		Name string `asun:"name"`
		Age  int64  `asun:"age"`
	}
	type GroupEntry struct {
		Key   string   `asun:"key"`
		Value []Person `asun:"value"`
	}
	type Groups struct {
		Groups []GroupEntry `asun:"groups"`
	}
	src := Groups{
		Groups: []GroupEntry{
			{Key: "teamA", Value: []Person{{Name: "Alice", Age: 30}, {Name: "Bob", Age: 28}}},
			{Key: "teamB", Value: []Person{{Name: "Carol", Age: 41}}},
		},
	}
	data, err := EncodeBinary(&src)
	if err != nil {
		t.Fatal(err)
	}
	var out Groups
	if err := DecodeBinary(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(src, out) {
		t.Fatalf("mismatch: %#v vs %#v", src, out)
	}
}

func TestAnnotatedWithOptional(t *testing.T) {
	type Record struct {
		ID    int64    `asun:"id"`
		Label *string  `asun:"label"`
		Score *float64 `asun:"score"`
	}
	typed := "{id@int,label@str,score@float}:(1,hello,95.5)"
	untyped := "{id,label,score}:(1,hello,95.5)"
	var r1, r2 Record
	if err := Decode([]byte(typed), &r1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untyped), &r2); err != nil {
		t.Fatal(err)
	}
	if *r1.Label != *r2.Label || *r1.Score != *r2.Score {
		t.Fatalf("mismatch")
	}
	typedNone := "{id@int,label@str,score@float}:(2,,)"
	untypedNone := "{id,label,score}:(2,,)"
	var r3, r4 Record
	if err := Decode([]byte(typedNone), &r3); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untypedNone), &r4); err != nil {
		t.Fatal(err)
	}
	if r3.Label != nil || r3.Score != nil {
		t.Fatalf("expected nil")
	}
}

func TestAnnotatedDeepNesting(t *testing.T) {
	type Task struct {
		Title string `asun:"title"`
		Done  bool   `asun:"done"`
	}
	type Project struct {
		Name  string `asun:"name"`
		Tasks []Task `asun:"tasks"`
	}
	type Team struct {
		Lead     string    `asun:"lead"`
		Projects []Project `asun:"projects"`
	}
	type Company struct {
		Name    string  `asun:"name"`
		Revenue float64 `asun:"revenue"`
		Team    Team    `asun:"team"`
	}
	typed := "{name@str,revenue@float,team@{lead@str,projects@[{name@str,tasks@[{title@str,done@bool}]}]}}:(Acme,500.5,(Alice,[(API,[(Design,true),(Code,false)])]))"
	untyped := "{name,revenue,team@{lead,projects@[{name,tasks@[{title,done}]}]}}:(Acme,500.5,(Alice,[(API,[(Design,true),(Code,false)])]))"
	var c1, c2 Company
	if err := Decode([]byte(typed), &c1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(untyped), &c2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c1, c2) {
		t.Fatalf("mismatch")
	}
	if c1.Name != "Acme" || c1.Team.Lead != "Alice" {
		t.Fatalf("unexpected")
	}
	if c1.Team.Projects[0].Tasks[0].Title != "Design" || !c1.Team.Projects[0].Tasks[0].Done {
		t.Fatalf("unexpected task")
	}
	if c1.Team.Projects[0].Tasks[1].Done {
		t.Fatalf("expected not done")
	}
}

func TestAnnotatedMixedPartial(t *testing.T) {
	type Mixed struct {
		ID     int64   `asun:"id"`
		Name   string  `asun:"name"`
		Score  float64 `asun:"score"`
		Active bool    `asun:"active"`
	}
	partial := "{id@int,name,score@float,active}:(1,Alice,95.5,true)"
	full := "{id@int,name@str,score@float,active@bool}:(1,Alice,95.5,true)"
	none := "{id,name,score,active}:(1,Alice,95.5,true)"
	var m1, m2, m3 Mixed
	if err := Decode([]byte(partial), &m1); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(full), &m2); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte(none), &m3); err != nil {
		t.Fatal(err)
	}
	if m1 != m2 || m2 != m3 {
		t.Fatalf("mismatch")
	}
}

func TestSerializerOutputIsUnannotated(t *testing.T) {
	u := User{ID: 1, Name: "Alice", Active: true}
	data, err := Encode(u)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if s != "{id,name,active}:(1,Alice,true)" {
		t.Fatalf("got %q", s)
	}
}

func TestEncodeTypedSimple(t *testing.T) {
	u := User{ID: 1, Name: "Alice", Active: true}
	got, err := EncodeTyped(u)
	if err != nil {
		t.Fatal(err)
	}
	expect := "{id@int,name@str,active@bool}:(1,Alice,true)"
	if string(got) != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
}

func TestEncodeTypedRoundtrip(t *testing.T) {
	u := User{ID: 42, Name: "Bob", Active: false}
	data, err := EncodeTyped(u)
	if err != nil {
		t.Fatal(err)
	}
	var u2 User
	if err := Decode(data, &u2); err != nil {
		t.Fatal(err)
	}
	if u != u2 {
		t.Fatalf("roundtrip failed")
	}
}

func TestEncodeTypedFloats(t *testing.T) {
	type ScoreF struct {
		ID    int64   `asun:"id"`
		Value float64 `asun:"value"`
		Label string  `asun:"label"`
	}
	s := ScoreF{ID: 1, Value: 95.5, Label: "good"}
	got, err := EncodeTyped(s)
	if err != nil {
		t.Fatal(err)
	}
	expect := "{id@int,value@float,label@str}:(1,95.5,good)"
	if string(got) != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
}

func TestEncodeTypedAllPrimitives(t *testing.T) {
	type All struct {
		B bool    `asun:"b"`
		I int64   `asun:"i"`
		U uint32  `asun:"u"`
		F float64 `asun:"f"`
		S string  `asun:"s"`
	}
	val := All{B: true, I: -42, U: 100, F: 3.14, S: "hello"}
	got, err := EncodeTyped(val)
	if err != nil {
		t.Fatal(err)
	}
	expect := "{b@bool,i@int,u@int,f@float,s@str}:(true,-42,100,3.14,hello)"
	if string(got) != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
}

func TestEncodeTypedOptional(t *testing.T) {
	type Opt struct {
		ID    int64    `asun:"id"`
		Label *string  `asun:"label"`
		Score *float64 `asun:"score"`
	}
	hello := "hello"
	sc := 95.5
	v1 := Opt{ID: 1, Label: &hello, Score: &sc}
	out1, err := EncodeTyped(v1)
	if err != nil {
		t.Fatal(err)
	}
	if string(out1) != "{id@int,label@str,score@float}:(1,hello,95.5)" {
		t.Fatalf("got %q", out1)
	}
	v2 := Opt{ID: 2, Label: nil, Score: nil}
	out2, err := EncodeTyped(v2)
	if err != nil {
		t.Fatal(err)
	}
	// The schema comes from the value: a nil pointer carries no hint.
	if string(out2) != "{id@int,label,score}:(2,,)" {
		t.Fatalf("got %q", out2)
	}
}

func TestEncodeTypedNestedStruct(t *testing.T) {
	type Dept struct {
		Title string `asun:"title"`
	}
	type Employee struct {
		Name   string `asun:"name"`
		Dept   Dept   `asun:"dept"`
		Active bool   `asun:"active"`
	}
	e := Employee{Name: "Alice", Dept: Dept{Title: "Engineering"}, Active: true}
	got, err := EncodeTyped(e)
	if err != nil {
		t.Fatal(err)
	}
	expect := "{name@str,dept@{title@str},active@bool}:(Alice,(Engineering),true)"
	if string(got) != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
}

func TestEncodeTypedVec(t *testing.T) {
	type RowT struct {
		ID    int64   `asun:"id"`
		Name  string  `asun:"name"`
		Score float64 `asun:"score"`
	}
	rows := []RowT{
		{ID: 1, Name: "Alice", Score: 95.5},
		{ID: 2, Name: "Bob", Score: 87.0},
	}
	untyped, err := Encode(rows)
	if err != nil {
		t.Fatal(err)
	}
	if string(untyped) != "[{id,name,score}]:(1,Alice,95.5),(2,Bob,87.0)" {
		t.Fatalf("got %q", untyped)
	}
	typed, err := EncodeTyped(rows)
	if err != nil {
		t.Fatal(err)
	}
	if string(typed) != "[{id@int,name@str,score@float}]:(1,Alice,95.5),(2,Bob,87.0)" {
		t.Fatalf("got %q", typed)
	}
	var r1, r2 []RowT
	if err := Decode(untyped, &r1); err != nil {
		t.Fatal(err)
	}
	if err := Decode(typed, &r2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("mismatch")
	}
}

func TestPrettyFormatSimple(t *testing.T) {
	type User struct {
		Name string `asun:"name"`
		Age  int    `asun:"age"`
	}
	u := User{Name: "Alice", Age: 30}
	p, err := EncodePretty(u)
	if err != nil {
		t.Fatal(err)
	}
	// Short enough to stay inline
	expected := "{name, age}:(Alice, 30)"
	if string(p) != expected {
		t.Fatalf("expected %q, got %q", expected, string(p))
	}
}

func TestPrettyFormatComplex(t *testing.T) {
	type Contact struct {
		Email string `asun:"email"`
		Phone string `asun:"phone"`
	}
	type Addr struct {
		City    string `asun:"city"`
		Zip     int    `asun:"zip"`
		Country string `asun:"country"`
	}
	type User struct {
		Id      int      `asun:"id"`
		Name    string   `asun:"name"`
		Active  bool     `asun:"active"`
		Contact Contact  `asun:"contact"`
		Addr    Addr     `asun:"addr"`
		Tags    []string `asun:"tags"`
	}

	u := User{
		Id: 1, Name: "John Smith", Active: true,
		Contact: Contact{Email: "john@example.com", Phone: "555-1234"},
		Addr:    Addr{City: "New York City", Zip: 10001, Country: "United States of America"},
		Tags:    []string{"dev", "admin", "reviewer"},
	}
	p, err := EncodePretty(u)
	if err != nil {
		t.Fatal(err)
	}
	result := string(p)
	// Should contain newlines (expanded)
	if !contains(result, "\n") {
		t.Fatalf("expected multi-line output, got single line: %s", result)
	}
	// Should contain indentation
	if !contains(result, "  ") {
		t.Fatalf("expected indentation: %s", result)
	}
	// Should be decodable
	var u2 User
	if err := Decode(p, &u2); err != nil {
		t.Fatalf("pretty output not decodable: %v\n%s", err, result)
	}
	if !reflect.DeepEqual(u, u2) {
		t.Fatalf("roundtrip mismatch")
	}
}

func TestPrettyFormatArray(t *testing.T) {
	type Row struct {
		Id   int    `asun:"id"`
		Name string `asun:"name"`
	}
	rows := []Row{{Id: 1, Name: "Alice"}, {Id: 2, Name: "Bob"}}
	p, err := EncodePretty(rows)
	if err != nil {
		t.Fatal(err)
	}
	result := string(p)
	// Each row on its own line
	if !contains(result, "\n") {
		t.Fatalf("expected multi-line: %s", result)
	}
	var rows2 []Row
	if err := Decode(p, &rows2); err != nil {
		t.Fatalf("pretty array not decodable: %v\n%s", err, result)
	}
	if !reflect.DeepEqual(rows, rows2) {
		t.Fatalf("roundtrip mismatch")
	}
}

func TestPrettyFormatTyped(t *testing.T) {
	type User struct {
		Name string `asun:"name"`
		Age  int    `asun:"age"`
	}
	u := User{Name: "Alice", Age: 30}
	p, err := EncodePrettyTyped(u)
	if err != nil {
		t.Fatal(err)
	}
	expected := "{name@str, age@int}:(Alice, 30)"
	if string(p) != expected {
		t.Fatalf("expected %q, got %q", expected, string(p))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// ============================================================================
// Format validation: {schema}: must be rejected for slices; [{schema}]: required
// ============================================================================

const badFmt = "{id,name}:\n  (1,Alice),\n  (2,Bob),\n  (3,Carol)"
const goodFmt = "[{id,name}]:\n  (1,Alice),\n  (2,Bob),\n  (3,Carol)"

func TestBadFormatAsSlice(t *testing.T) {
	var rows []Row
	err := Decode([]byte(badFmt), &rows)
	if err == nil {
		t.Fatalf("should reject {schema}: format for slice, got %+v", rows)
	}
}

func TestBadFormatTrailingRows(t *testing.T) {
	// {schema}:(row),(row),(row) — first struct decoded, trailing rows are trailing chars
	var r Row
	err := Decode([]byte(badFmt), &r)
	if err == nil {
		t.Fatalf("should reject trailing rows after single struct decode, got %+v", r)
	}
}

func TestGoodFormatAsSlice(t *testing.T) {
	var rows []Row
	if err := Decode([]byte(goodFmt), &rows); err != nil {
		t.Fatalf("should accept [{schema}]: for slice: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3, got %d", len(rows))
	}
	if rows[0].ID != 1 || rows[0].Name != "Alice" {
		t.Fatalf("unexpected: %+v", rows[0])
	}
	if rows[2].Name != "Carol" {
		t.Fatalf("unexpected: %+v", rows[2])
	}
}

func TestGoodFormatSingleStruct(t *testing.T) {
	// {id,name}: (1, Alice) — single struct, one tuple: MUST succeed
	var r Row
	if err := Decode([]byte("{id,name}:(1,Alice)"), &r); err != nil {
		t.Fatalf("should accept single struct with one tuple: %v", err)
	}
	if r.ID != 1 || r.Name != "Alice" {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestGoodFormatVecSingleItem(t *testing.T) {
	// [{id,name}]: (1, Alice) — array schema, one tuple: MUST succeed
	var rows []Row
	if err := Decode([]byte("[{id,name}]:(1,Alice)"), &rows); err != nil {
		t.Fatalf("should accept [{schema}]: with single tuple: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1, got %d", len(rows))
	}
	if rows[0].ID != 1 || rows[0].Name != "Alice" {
		t.Fatalf("unexpected: %+v", rows[0])
	}
}

func TestBadFormatExtraTuples(t *testing.T) {
	// Two tuples without vec wrapper
	var r Row
	err := Decode([]byte("{id,name}:(10,Dave),(11,Eve)"), &r)
	if err == nil {
		t.Fatalf("should reject trailing tuple, got %+v", r)
	}
}

func TestBadFormatStructAsSliceMissingBracket(t *testing.T) {
	// Many rows, no [] wrapper
	var rows []Row
	err := Decode([]byte("{id,name}:(1,A),(2,B),(3,C),(4,D),(5,E)"), &rows)
	if err == nil {
		t.Fatalf("should reject {schema}: without [] for slice, got %v rows", len(rows))
	}
}

// ============================================================================
// encodePretty -> decode roundtrip tests (complex cases)
// ============================================================================

func TestPrettyRoundtripSimple(t *testing.T) {
	u := User{ID: 42, Name: "Alice", Active: true}
	p, err := EncodePretty(u)
	if err != nil {
		t.Fatal(err)
	}
	var u2 User
	if err := Decode(p, &u2); err != nil {
		t.Fatalf("pretty output not decodable: %v\n%s", err, p)
	}
	if u != u2 {
		t.Fatalf("mismatch: %+v != %+v", u, u2)
	}
}

func TestPrettyRoundtripTyped(t *testing.T) {
	u := User{ID: 7, Name: "Bob", Active: false}
	p, err := EncodePrettyTyped(u)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(p), "@str") || !contains(string(p), "@int") {
		t.Fatalf("expected typed annotations in %q", string(p))
	}
	var u2 User
	if err := Decode(p, &u2); err != nil {
		t.Fatalf("typed pretty not decodable: %v\n%s", err, p)
	}
	if u != u2 {
		t.Fatalf("mismatch: %+v != %+v", u, u2)
	}
}

func TestPrettyRoundtripSlice(t *testing.T) {
	rows := []Row{{ID: 1, Name: "Alice"}, {ID: 2, Name: "Bob"}, {ID: 3, Name: "Carol"}}
	p, err := EncodePretty(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(p), "\n") {
		t.Fatalf("expected multi-line: %s", p)
	}
	var rows2 []Row
	if err := Decode(p, &rows2); err != nil {
		t.Fatalf("pretty slice not decodable: %v\n%s", err, p)
	}
	if len(rows2) != 3 || rows2[0] != rows[0] || rows2[2] != rows[2] {
		t.Fatalf("mismatch: %+v != %+v", rows2, rows)
	}
}

func TestPrettyRoundtripScore(t *testing.T) {
	type ScoreRow struct {
		ID    int64   `asun:"id"`
		Value float64 `asun:"value"`
		Label string  `asun:"label"`
	}
	rows := []ScoreRow{{1, 95.5, "excellent"}, {2, 72.3, "good"}, {3, 40.0, "fail"}}
	p, err := EncodePretty(rows)
	if err != nil {
		t.Fatal(err)
	}
	var rows2 []ScoreRow
	if err := Decode(p, &rows2); err != nil {
		t.Fatalf("pretty score slice not decodable: %v\n%s", err, p)
	}
	if len(rows2) != 3 || rows2[0].Value != 95.5 || rows2[0].Label != "excellent" {
		t.Fatalf("mismatch: %+v", rows2)
	}
}

func TestPrettyRoundtripNestedStruct(t *testing.T) {
	type Addr struct {
		City    string `asun:"city"`
		Country string `asun:"country"`
	}
	type Person struct {
		ID   int64  `asun:"id"`
		Name string `asun:"name"`
		Addr Addr   `asun:"addr"`
	}
	p1 := Person{ID: 1, Name: "Alice", Addr: Addr{City: "New York", Country: "US"}}
	out, err := EncodePretty(p1)
	if err != nil {
		t.Fatal(err)
	}
	var p2 Person
	if err := Decode(out, &p2); err != nil {
		t.Fatalf("pretty nested not decodable: %v\n%s", err, out)
	}
	if p1.Addr.City != p2.Addr.City || p1.Addr.Country != p2.Addr.Country {
		t.Fatalf("nested mismatch: %+v != %+v", p1, p2)
	}
}

func TestPrettyRoundtripWithOptional(t *testing.T) {
	label := "hello"
	in := Item{ID: 5, Label: &label}
	out, err := EncodePretty(in)
	if err != nil {
		t.Fatal(err)
	}
	var out2 Item
	if err := Decode(out, &out2); err != nil {
		t.Fatalf("pretty optional not decodable: %v\n%s", err, out)
	}
	if out2.ID != 5 || out2.Label == nil || *out2.Label != label {
		t.Fatalf("mismatch: %+v", out2)
	}
}

func TestPrettyRoundtripLargeSlice(t *testing.T) {
	rows := make([]Row, 50)
	for i := range rows {
		rows[i] = Row{ID: int64(i + 1), Name: "user"}
	}
	p, err := EncodePretty(rows)
	if err != nil {
		t.Fatal(err)
	}
	var rows2 []Row
	if err := Decode(p, &rows2); err != nil {
		t.Fatalf("pretty large slice not decodable: %v", err)
	}
	if len(rows2) != 50 || rows2[49].ID != 50 {
		t.Fatalf("mismatch len=%d last=%+v", len(rows2), rows2[49])
	}
}

// ─── Typed primitive slice fields ────────────────────────────────────────────

type WithBoolSlice struct {
	Name  string `asun:"name"`
	Flags []bool `asun:"flags"`
}

type WithIntSlice struct {
	Name string  `asun:"name"`
	Nums []int64 `asun:"nums"`
}

type WithStrSlice struct {
	Name string   `asun:"name"`
	Tags []string `asun:"tags"`
}

func TestEncodeTypedBoolSliceField(t *testing.T) {
	v := WithBoolSlice{Name: "test", Flags: []bool{true, false, true}}
	out, err := EncodeTyped(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !contains(s, "flags@[bool]") {
		t.Fatalf("expected flags@[bool], got %s", s)
	}
	var decoded WithBoolSlice
	if err := Decode(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Flags, v.Flags) {
		t.Fatalf("flags mismatch: %v vs %v", decoded.Flags, v.Flags)
	}
}

func TestEncodeTypedIntSliceField(t *testing.T) {
	v := WithIntSlice{Name: "test", Nums: []int64{1, 2, 3}}
	out, err := EncodeTyped(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !contains(s, "nums@[int]") {
		t.Fatalf("expected nums@[int], got %s", s)
	}
	var decoded WithIntSlice
	if err := Decode(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Nums, v.Nums) {
		t.Fatalf("nums mismatch: %v vs %v", decoded.Nums, v.Nums)
	}
}

func TestEncodeTypedStrSliceField(t *testing.T) {
	v := WithStrSlice{Name: "test", Tags: []string{"a", "b"}}
	out, err := EncodeTyped(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !contains(s, "tags@[str]") {
		t.Fatalf("expected tags@[str], got %s", s)
	}
}

func TestEncodeTypedEmptyBoolSlice(t *testing.T) {
	v := WithBoolSlice{Name: "test", Flags: []bool{}}
	out, err := EncodeTyped(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// The schema comes from the value: an empty slice has no element hint.
	if s != "{name@str,flags@[]}:(test,[])" {
		t.Fatalf("expected flags@[] for empty slice, got %s", s)
	}
}

func TestEncodePrettyTypedBoolSliceField(t *testing.T) {
	v := WithBoolSlice{Name: "test", Flags: []bool{true, false}}
	out, err := EncodePrettyTyped(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !contains(s, "[bool]") {
		t.Fatalf("expected [bool] in pretty typed, got %s", s)
	}
}

// ─── Field names with +/- in decode ─────────────────────────────────────────

type PlusMinusFields struct {
	LowPriority string `asun:"lowPriorityEIR+CIR"`
	AB          string `asun:"a-b"`
	Name        string `asun:"name"`
}

type QuotedSchemaFields struct {
	IDUUID  int64  `asun:"id uuid"`
	Numeric string `asun:"65"`
	Special bool   `asun:"{}[]@\""`
}

func TestDecodeFieldNamesWithPlusMinus(t *testing.T) {
	input := []byte(`{"lowPriorityEIR+CIR","a-b",name}:(42,hello,Alice)`)
	var v PlusMinusFields
	if err := Decode(input, &v); err != nil {
		t.Fatal(err)
	}
	if v.LowPriority != "42" {
		t.Fatalf("LowPriority = %q, want 42", v.LowPriority)
	}
	if v.AB != "hello" {
		t.Fatalf("AB = %q, want hello", v.AB)
	}
	if v.Name != "Alice" {
		t.Fatalf("Name = %q, want Alice", v.Name)
	}
}

// A bare field name is `[A-Za-z0-9_]+`; anything else must be quoted.
func TestDecodeFieldNamesPlusMinusBareRejected(t *testing.T) {
	input := []byte("{lowPriorityEIR+CIR,a-b,name}:(42,hello,Alice)")
	var v PlusMinusFields
	if err := Decode(input, &v); err == nil {
		t.Fatalf("expected an error for bare names with +/-, got %+v", v)
	}
	out, err := Encode(PlusMinusFields{LowPriority: "42", AB: "hello", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"lowPriorityEIR+CIR","a-b",name}:("42",hello,Alice)` {
		t.Fatalf("got %s", out)
	}
}

func TestQuotedSchemaFieldNamesAcrossAPIs(t *testing.T) {
	v := QuotedSchemaFields{IDUUID: 1, Numeric: "Alice", Special: true}

	text, err := Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "{\"id uuid\",\"65\",\"{}[]@\\\"\"}:(1,Alice,true)" {
		t.Fatalf("Encode got %s", text)
	}
	var out QuotedSchemaFields
	if err := Decode(text, &out); err != nil {
		t.Fatal(err)
	}
	if out != v {
		t.Fatalf("Decode mismatch: %#v != %#v", out, v)
	}

	typed, err := EncodeTyped(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(typed) != "{\"id uuid\"@int,\"65\"@str,\"{}[]@\\\"\"@bool}:(1,Alice,true)" {
		t.Fatalf("EncodeTyped got %s", typed)
	}
	out = QuotedSchemaFields{}
	if err := Decode(typed, &out); err != nil {
		t.Fatal(err)
	}
	if out != v {
		t.Fatalf("typed Decode mismatch: %#v != %#v", out, v)
	}

	pretty, err := EncodePretty(v)
	if err != nil {
		t.Fatal(err)
	}
	out = QuotedSchemaFields{}
	if err := Decode(pretty, &out); err != nil {
		t.Fatal(err)
	}
	if out != v {
		t.Fatalf("pretty Decode mismatch: %#v != %#v", out, v)
	}

	prettyTyped, err := EncodePrettyTyped(v)
	if err != nil {
		t.Fatal(err)
	}
	out = QuotedSchemaFields{}
	if err := Decode(prettyTyped, &out); err != nil {
		t.Fatal(err)
	}
	if out != v {
		t.Fatalf("pretty typed Decode mismatch: %#v != %#v", out, v)
	}

	bin, err := EncodeBinary(v)
	if err != nil {
		t.Fatal(err)
	}
	out = QuotedSchemaFields{}
	if err := DecodeBinary(bin, &out); err != nil {
		t.Fatal(err)
	}
	if out != v {
		t.Fatalf("binary mismatch: %#v != %#v", out, v)
	}
}

func TestDecodeQuotedSchemaFieldNames(t *testing.T) {
	input := []byte("{\"id uuid\"@int,\"65\"@str,\"{}[]@\\\"\"@bool}:(1,Alice,true)")
	var v QuotedSchemaFields
	if err := Decode(input, &v); err != nil {
		t.Fatal(err)
	}
	if v.IDUUID != 1 || v.Numeric != "Alice" || !v.Special {
		t.Fatalf("unexpected decode: %#v", v)
	}
}

func TestDecodeRejectsInvalidSchemaTypes(t *testing.T) {
	var out struct {
		ID    int64   `asun:"id"`
		Name  string  `asun:"name"`
		Score float64 `asun:"score"`
		Alive bool    `asun:"alive"`
	}

	for _, input := range []string{
		"{id@numx,name@str}:(1,Alice)",
		"{id@int,name@textx}:(1,Alice)",
		"{id@int,score@decimalx}:(1,3.5)",
		"{id@int,alive@flagx}:(1,true)",
		"{items@[textx]}:([Alice])",
		"{profile@{name@textx}}:((Alice))",
	} {
		if err := Decode([]byte(input), &out); err == nil {
			t.Fatalf("expected alias rejection for %q", input)
		}
	}
}

# asun-go

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.24+-00ADD8.svg)](https://go.dev)

High-performance Go support for [ASUN](https://github.com/asunLab/asun), a schema-driven format for compact structured data.

[中文文档](https://github.com/asunLab/asun-go/blob/main/README_CN.md)

## Why ASUN?

**json**

Standard JSON repeats every field name in every record. When you send structured data to an LLM, over an API, or across services, that repetition wastes tokens, bytes, and attention:

```json
[
  { "id": 1, "name": "Alice", "active": true },
  { "id": 2, "name": "Bob", "active": false },
  { "id": 3, "name": "Carol", "active": true }
]
```

**asun**

ASUN declares the schema **once** and streams data as compact tuples:

```asun
[{id, name, active}]:
  (1,Alice,true),
  (2,Bob,false),
  (3,Carol,true)
```

**Fewer tokens. Smaller payloads. Clearer structure, and faster parsing than repeated-object JSON.**

---

## Highlights

- Standard library only
- Current API uses `Encode` / `Decode`, not the older `Marshal` / `Unmarshal` names
- Text, pretty text, and binary formats
- Struct tags via `asun:"..."`, with `json` tag fallback
- No native `map` / dictionary field syntax; model key-value data as slices of entry structs
- Good fit for LLM payloads, internal services, logs, and fixtures

## Install

```bash
go get github.com/asunLab/asun-go
```

## Quick Start

```go
package main

import (
    "fmt"
    asun "github.com/asunLab/asun-go"
)

type User struct {
    ID     int64  `asun:"id"`
    Name   string `asun:"name"`
    Active bool   `asun:"active"`
}

func main() {
    user := User{ID: 1, Name: "Alice", Active: true}

    text, _ := asun.Encode(&user)
    fmt.Println(string(text))
    // {id,name,active}:(1,Alice,true)

    typed, _ := asun.EncodeTyped(&user)
    fmt.Println(string(typed))
    // {id@int,name@str,active@bool}:(1,Alice,true)

    var decoded User
    _ = asun.Decode(text, &decoded)
}
```

### Encode a slice

```go
users := []User{
    {ID: 1, Name: "Alice", Active: true},
    {ID: 2, Name: "Bob", Active: false},
}

text, _ := asun.Encode(users)
typed, _ := asun.EncodeTyped(users)

var decoded []User
_ = asun.Decode(text, &decoded)
```

### Pretty and binary output

```go
pretty, _ := asun.EncodePretty(users)
prettyTyped, _ := asun.EncodePrettyTyped(users)
bin, _ := asun.EncodeBinary(users)

var decoded []User
_ = asun.DecodeBinary(bin, &decoded)
```

### Model key-value data with entry structs

```go
type EnvEntry struct {
    Key   string `asun:"key"`
    Value string `asun:"value"`
}

type Config struct {
    Name string     `asun:"name"`
    Env  []EnvEntry `asun:"env"`
}
```

Typed ASUN output:

```text
{name@str,env@[{key@str,value@str}]}:(api,[(RUST_LOG,debug),(PORT,"8080")])
```

## Current API

| Function                             | Purpose            |
| ------------------------------------ | ------------------ |
| `Encode` / `EncodeTyped`             | Encode to text     |
| `Decode`                             | Decode from text   |
| `EncodePretty` / `EncodePrettyTyped` | Pretty text output |
| `EncodeBinary`                       | Encode to binary   |
| `DecodeBinary`                       | Decode from binary |
| `DecodeBinaryExact`                  | Decode from binary, rejecting trailing bytes |
| `PrettyFormat`                       | Re-indent compact ASUN text |

## Run Examples

```bash
go test ./...
go run ./examples/basic
go run ./examples/complex
go run ./examples/bench
```

## Contributors

- [Athan](https://github.com/athxx)

## Benchmarks

Run:

```bash
go run ./examples/bench
```

The benchmark output now follows the same layout as the C and C++ versions:

```text
Serialize:   JSON    16.22ms | ASUN    16.80ms (1x) | BIN    15.02ms (1.1x)
Deserialize: JSON   111.90ms | ASUN    35.50ms (3.2x) | BIN    35.10ms (3.2x)
Size:        JSON   218737 B | ASUN    84861 B (39%) | BIN    85282 B (39%)
```

## License

MIT

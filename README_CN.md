# asun-go

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.24+-00ADD8.svg)](https://go.dev)

面向 [ASUN](https://github.com/asunLab/asun) 的高性能 Go 实现。ASUN 是一种适合紧凑结构化数据的 Schema 驱动格式。

[English](https://github.com/asunLab/asun-go/blob/main/README.md)

## 为什么选择 ASUN

**json**

标准 JSON 会在每条记录里重复所有字段名。无论是发给 LLM、通过 API 传输，还是服务之间交换数据，这种重复都会浪费 Token、带宽和阅读成本：

```json
[
  { "id": 1, "name": "Alice", "active": true },
  { "id": 2, "name": "Bob", "active": false },
  { "id": 3, "name": "Carol", "active": true }
]
```

**asun**

ASUN 只声明 **一次** Schema，后续每一行只保留值：

```asun
[{id, name, active}]:
  (1,Alice,true),
  (2,Bob,false),
  (3,Carol,true)
```

**这通常意味着更少的 token、更小的体积，更清晰的结构, 以及比重复键名 JSON 更快的解析。**

---

## 特性

- 仅依赖 Go 标准库
- 当前 API 是 `Encode` / `Decode`，不再是旧文档里的 `Marshal` / `Unmarshal`
- 同时支持文本、格式化文本和二进制格式
- 通过 `asun:"..."` struct tag 定义字段名，并回退支持 `json` tag
- 适合 LLM 载荷、内部服务、日志和测试数据

## 安装

```bash
go get github.com/asunLab/asun-go
```

## 快速开始

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

### 编码切片

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

### 格式化文本和二进制

```go
pretty, _ := asun.EncodePretty(users)
prettyTyped, _ := asun.EncodePrettyTyped(users)
bin, _ := asun.EncodeBinary(users)

var decoded []User
_ = asun.DecodeBinary(bin, &decoded)
```

### 用 entry struct 表达键值集合

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

对应的带类型 ASUN 文本：

```text
{name@str,env@[{key@str,value@str}]}:(api,[(RUST_LOG,debug),(PORT,"8080")])
```

## 当前 API

| 函数                                 | 作用             |
| ------------------------------------ | ---------------- |
| `Encode` / `EncodeTyped`             | 编码为文本       |
| `Decode`                             | 从文本解码       |
| `EncodePretty` / `EncodePrettyTyped` | 生成更易读的文本 |
| `EncodeBinary`                       | 编码为二进制     |
| `DecodeBinary`                       | 从二进制解码     |
| `DecodeBinaryExact`                  | 从二进制解码，拒绝多余的尾部字节 |
| `PrettyFormat`                       | 把紧凑 ASUN 文本重新缩进 |

## 运行示例

```bash
go test ./...
go run ./examples/basic
go run ./examples/complex
go run ./examples/bench
```

## Contributors

- [Athan](https://github.com/athxx)

## Benchmarks

可通过下面命令运行：

```bash
go run ./examples/bench
```

输出格式与 C / C++ 版本保持一致，例如：

```text
Serialize:   JSON    16.22ms | ASUN    16.80ms (1x) | BIN    15.02ms (1.1x)
Deserialize: JSON   111.90ms | ASUN    35.50ms (3.2x) | BIN    35.10ms (3.2x)
Size:        JSON   218737 B | ASUN    84861 B (39%) | BIN    85282 B (39%)
```

## 许可证

MIT

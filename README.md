# braille-api

盲文印前编码 API：在压印前把文本转换为可复核的六点单元流。纯后端服务，Go 1.25 + Gin。

## 编码规则

- 点位 1–6 的位权依次为 1、2、4、8、16、32，单元掩码为各凸点位权之和。
- A–Z 掩码固定为 `1, 3, 9, 25, 17, 11, 27, 19, 10, 26, 5, 7, 13, 29, 21, 15, 31, 23, 14, 30, 37, 39, 58, 45, 61, 53`。
- 每个小写字母前输出大写指示符 `32`（点 6），`prefix_type=capital`。
- 每段连续数字仅在首位前输出数字指示符 `60`（点 3、4、5、6），`prefix_type=number`；空格、换行或字母都会结束数字段。
- 数字复用字母掩码：1–9 对应 A–I，0 对应 J。
- 半角空格输出空白单元 `0`。
- 换行不产生掩码，唯一序列化为 `kind=newline, prefix_type=none, cells=[]`；其余字符 `kind=character`。
- 仅接受 A–Z、a–z、0–9、半角空格与换行；`source_index` 按 Unicode 码点从 0 起算。

## API

### `POST /encode`

请求：

```json
{"text": "A1b 23\nZz0"}
```

成功 `200`：每个来源字符一条记录。

```json
{"items":[
  {"source_index":0,"source":"A","kind":"character","prefix_type":"none","cells":[1]},
  {"source_index":1,"source":"1","kind":"character","prefix_type":"number","cells":[60,1]},
  {"source_index":2,"source":"b","kind":"character","prefix_type":"capital","cells":[32,3]},
  {"source_index":3,"source":" ","kind":"character","prefix_type":"none","cells":[0]},
  {"source_index":4,"source":"2","kind":"character","prefix_type":"number","cells":[60,3]},
  {"source_index":5,"source":"3","kind":"character","prefix_type":"none","cells":[9]},
  {"source_index":6,"source":"\n","kind":"newline","prefix_type":"none","cells":[]},
  {"source_index":7,"source":"Z","kind":"character","prefix_type":"none","cells":[53]},
  {"source_index":8,"source":"z","kind":"character","prefix_type":"capital","cells":[32,53]},
  {"source_index":9,"source":"0","kind":"character","prefix_type":"number","cells":[60,26]}
]}
```

校验失败整份拒绝，返回 `422`，不携带任何部分编码：

| 场景 | code | 说明 |
| --- | --- | --- |
| 空文本 | `empty_text` | `{"error":{"code":"empty_text","message":"..."}}` |
| 超过 2000 个码点 | `text_too_long` | 附带 `length` 与 `limit` |
| 含汉字、标点、制表符、回车等 | `invalid_character` | 附带首个非法字符的 `source_index` 与 `source` |

请求体不是合法 JSON 或 `text` 不是字符串时返回 `400 bad_request`。

### `POST /layout`

排版预览：先完整执行与 `/encode` 相同的编码，再按压印设备行宽把记录装入各行，供印前确认换行效果。

请求：`text` 同上，`cells_per_line` 为每行单元数，须为 2–80 的整数（下限 2 保证最宽的双单元字符——指示符加主体——总能独占一行）。

```json
{"text": "Ab\n12", "cells_per_line": 2}
```

成功 `200` 返回 `lines`，每行含从零递增的 `line_index` 与 `items`；`items` 直接复用 `/encode` 的记录结构（含 `source_index`）。

```json
{"lines":[
  {"line_index":0,"items":[
    {"source_index":0,"source":"A","kind":"character","prefix_type":"none","cells":[1]}]},
  {"line_index":1,"items":[
    {"source_index":1,"source":"b","kind":"character","prefix_type":"capital","cells":[32,3]},
    {"source_index":2,"source":"\n","kind":"newline","prefix_type":"none","cells":[]}]},
  {"line_index":2,"items":[
    {"source_index":3,"source":"1","kind":"character","prefix_type":"number","cells":[60,1]}]},
  {"line_index":3,"items":[
    {"source_index":4,"source":"2","kind":"character","prefix_type":"none","cells":[3]}]}
]}
```

装行规则：

- 同一字符的指示符与主体单元不可拆开：放不下的字符整体移到下一行（如上例 `b` 的 `32,3`）。
- 自动折行不改写任何记录：数字段跨行延续时不补数字指示符，大写指示符与 `source_index` 保持原样（如上例 `2` 仍是 `prefix_type=none, cells=[3]`）。
- 显式换行立即结束当前行，换行记录作为该行的最后一条；连续或末尾换行保留可见空行（空行 `items` 为 `[]` 或仅含换行记录）。

错误：

| 场景 | 状态码 | code | 说明 |
| --- | --- | --- | --- |
| 行宽缺失、非整数或越界 | `422` | `invalid_line_width` | 附带 `min: 2, max: 80` 指出允许范围 |
| 文本问题（空、超长、非法字符） | `422` | 与 `/encode` 相同 | 编码先行校验，不泄露局部排版结果 |
| 畸形 JSON、`text` 非字符串 | `400` | `bad_request` | 与 `/encode` 相同 |

### `GET /healthz`

存活探针，返回 `200 {"status":"ok"}`。

## 本地开发

```sh
go test ./...          # 单元测试：状态切换、边界、错误路径
go run ./cmd/server    # 监听 :8080，可用 PORT 覆盖
```

## Docker Compose

```sh
docker compose up api                 # 启动 API，宿主端口默认 8080
API_PORT=9000 docker compose up api   # 宿主端口由 API_PORT 覆盖
docker compose up --exit-code-from verify verify   # 一次性验收：跑完即退出并回传退出码
```

`verify` 服务等待 `api` 健康后执行验收套件（编码金样、数字段状态切换、0–9 映射、空文本/超长/非法字符 422、畸形 JSON 400、排版金样、双单元字符临界折行、数字段跨自动行延续、连续/末尾换行空行、行宽 422、响应确定性等），全部通过则以 0 退出，否则非 0。

两个服务由同一个多阶段 Dockerfile 构建：共享的 `build` 阶段编译出两个二进制，`server`/`verify` 两个 target 分别产出 `braille-api:local` 与 `braille-verify:local` 两个独立镜像，并行构建共享缓存且互不覆盖。

## 结构

```
cmd/server    API 入口（PORT 环境变量，默认 8080）
cmd/verify    一次性验收客户端（API_URL 环境变量）
internal/braille  编码核心（校验 + 状态机）与排版装行
internal/api      Gin 路由与处理器
```

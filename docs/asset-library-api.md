# 素材库 API 接入文档

面向下游接入方（应用开发者 / 企业内部系统）。素材库用于管理图片、视频、音频素材，并将其自动同步到已配置的上游素材库渠道（火山方舟 Assets API 兼容渠道），随后在视频生成等任务中以 `asset://` 引用使用。

除普通素材外，素材库还支持**真人人像**：终端用户本人完成一次活体认证后，平台为其接入一个专属素材组，之后即可向该组上传本人素材并用于生成。详见「[真人人像认证](#真人人像认证)」。

## 目录

- [核心概念](#核心概念)
- [接入准备](#接入准备)
- [统一接口约定](#统一接口约定)
- [上传素材文件](#上传素材文件)
- [素材组管理](#素材组管理)
- [素材管理](#素材管理)
- [真人人像认证](#真人人像认证)
- [同步状态说明](#同步状态说明)
- [在生成任务中引用素材](#在生成任务中引用素材)
- [错误码](#错误码)
- [端到端接入示例](#端到端接入示例)

> 相关文档：[视频生成 API 文档](video-generation-api.md)（任务创建/查询、`asset://` 引用、参数说明）

## 核心概念

| 概念 | 说明 |
|------|------|
| **素材组（Asset Group）** | 素材的容器，Id 形如 `group-na-<32位hex>`。创建素材时必须归属某个组。 |
| **素材（Asset）** | 一条图片/视频/音频记录，Id 形如 `asset-na-<32位hex>`。素材通过一个公网 URL 创建（通常是先调上传接口拿到的 URL）。 |
| **副本（Replica）** | 素材在上游渠道的一份同步记录。一个素材在每个启用的渠道各有一个副本，状态相互独立。 |
| **组类型（GroupType）** | 组分为两类：`AIGC`（普通素材，可自行创建）与 `LivenessFace`（真人素材，由活体认证产生，见「[真人人像认证](#真人人像认证)」）。 |
| **ProjectName** | 逻辑项目名，默认 `default`，可用于多租户/多项目隔离。所有请求均可选传入。 |

典型流程：

```
上传文件 ──► 拿到 URL ──► 创建素材(组) ──► 自动同步上游 ──► 状态变 ready ──► 生成任务中 asset:// 引用
```

## 接入准备

1. **Base URL**：你的 New API 网关地址，例如 `https://your-gateway.example.com`。下文以 `{$BASE}` 代指。
2. **鉴权**：所有接口在 Header 中携带令牌：

   ```
   Authorization: Bearer <你的令牌>
   ```

   令牌可以是控制台创建的 **API 令牌**（`sk-` 开头）或 **访问令牌（Dashboard Access Token）**。
3. **前提**：网关管理员已至少启用一个素材库渠道，否则创建接口返回 `AssetLibraryUnavailable`。

## 统一接口约定

素材组与素材管理共用一个入口，采用 Action 风格（与火山方舟 Assets API 兼容）：

```
POST {$BASE}/api/asset-library?Action=<动作名>&Version=2024-01-01
Content-Type: application/json

{ ...请求体... }
```

支持的动作：

| Action | 说明 |
|--------|------|
| `CreateAssetGroup` | 创建素材组 |
| `ListAssetGroups` | 分页列出素材组 |
| `GetAssetGroup` | 查询单个素材组 |
| `UpdateAssetGroup` | 更新组名称/描述 |
| `DeleteAssetGroup` | 删除素材组（须先清空素材） |
| `CreateAsset` | 创建素材并同步上游 |
| `ListAssets` | 分页列出素材 |
| `GetAsset` | 查询单个素材（含状态刷新） |
| `UpdateAsset` | 更新素材名称 |
| `DeleteAsset` | 删除素材（本地 + 上游） |
| `CreateFaceVerification` | 发起真人人像活体认证（见「[真人人像认证](#真人人像认证)」） |
| `GetFaceVerification` | 查询真人认证会话状态 |

### 响应信封

```json
{
  "ResponseMetadata": {
    "RequestId": "20260822... ",
    "Action": "CreateAsset",
    "Version": "2024-01-01",
    "Service": "asset",
    "Region": "cn-north-1",
    "Error": { "Code": "...", "Message": "..." }
  },
  "Result": { ... }
}
```

- 成功：HTTP 200，`Error` 缺省，结果在 `Result`。
- 失败：非 200，错误信息在 `ResponseMetadata.Error`。
- `Version` 固定为 `2024-01-01`，否则返回 `InvalidParameter.Version`。

### 分页与排序（List 类接口）

| 参数 | 类型 | 说明 |
|------|------|------|
| `PageNumber` | int | 默认 1 |
| `PageSize` | int | 默认 10，1–100 |
| `SortBy` | string | `CreateTime` / `UpdateTime`（ListAssets 额外支持 `GroupId`） |
| `SortOrder` | string | `Asc` / `Desc` |
| `Filter` | object | 可按 `GroupIds`、`GroupType`、`Name`、`AssetType`、`Statuses` 过滤 |

## 上传素材文件

`CreateAsset` 需要一个公网可访问的 URL。本地文件先走此上传接口换取 URL。

```
POST {$BASE}/api/asset/upload
Content-Type: multipart/form-data
Authorization: Bearer <令牌>

file=<二进制文件>
```

响应：

```json
{
  "success": true,
  "message": "",
  "data": {
    "Url": "https://your-gateway.example.com/api/asset/files/ab/abcd1234....mp4",
    "AssetType": "Video",
    "Size": 10485760
  }
}
```

`data.Url` 直接作为下一步 `CreateAsset` 的 `URL` 参数使用。

### 文件限制

- **大小**：默认单文件 ≤ 100 MB（网关可通过 `ASSET_UPLOAD_MAX_MB` 调整）。
- **格式**：按文件头自动识别（MIME 嗅探），支持的类型：

  | 类型 | 格式 |
  |------|------|
  | Image | png / jpeg / webp / gif |
  | Video | mp4 / webm / mov |
  | Audio | mp3 / wav / ogg / m4a |

- **视频约束**（上传时本地预检，不达标直接拒绝）：
  - 帧率：**23.8 – 60 FPS**
  - 时长：**1.8 – 30.2 秒**

  超出范围返回 HTTP 400，`message` 形如：`视频帧率为 20.1 FPS，需在 23.8–60 FPS 之间，请重新转码后上传`。
- 上传有频率限制（服务端限流），失败可稍后重试。

## 素材组管理

### CreateAssetGroup

```json
{
  "Name": "营销素材-8月",
  "Description": "8月投放用素材",
  "GroupType": "AIGC",
  "ProjectName": "default"
}
```

- `Name` 必填，≤ 64 字符；`Description` ≤ 300 字符；`ProjectName` 默认 `default`。
- `GroupType` 只支持 `AIGC`（可省略，大小写不敏感）。真人素材组（`LivenessFace`）由活体认证流程产生，**不能通过本接口创建**：传入其他取值返回 HTTP 400 `InvalidParameter.GroupType`。
- 组名在**同一上游渠道内需唯一**，重名会同步失败并进入自动重试。

结果：`{ "Id": "group-na-..." }`（管理员令牌额外返回 `Replication` 同步摘要）。

### ListAssetGroups / GetAssetGroup

请求示例：

```json
{ "PageNumber": 1, "PageSize": 20, "Filter": { "Name": "营销" } }
```

`GetAssetGroup` 传 `{ "Id": "group-na-..." }`。

组条目字段：`Id` / `Name` / `Description` / `GroupType` / `ProjectName` / `CreateTime` / `UpdateTime`（RFC3339 UTC）。

### UpdateAssetGroup / DeleteAssetGroup

- 更新：`{ "Id": "...", "Name": "新名称", "Description": "新描述" }`（至少一项）。
- 删除：`{ "Id": "..." }`。**组内仍有素材时返回 409 `AssetGroupNotEmpty`**，须先删光素材。

### 真人素材组（GroupType=LivenessFace）

真人素材组不由调用方创建：它是终端用户完成活体认证后，由平台接入的、**归属于该用户本人**的素材组。

| 项 | 说明 |
|------|------|
| 产生方式 | 完成一次真人认证后自动出现（见「[真人人像认证](#真人人像认证)」） |
| Id 形态 | 与普通组一致，形如 `group-na-<32位hex>` |
| 归属 | 只属于发起认证的那个账号，其他账号查询返回 404 |
| 素材操作 | 与普通组一致，可直接 `CreateAsset` 上传本人素材 |
| 删除 | 删除只清除平台侧记录，**不会撤销已完成的认证**，但该组随即不可再使用 |

真人素材组与其素材对应的生成渠道由平台自动维护，调用方无需（也无法）为引用真人素材的请求指定渠道。

## 素材管理

### CreateAsset

```json
{
  "GroupId": "group-na-...",
  "URL": "https://your-gateway.example.com/api/asset/files/ab/abcd....mp4",
  "AssetType": "Video",
  "Name": "产品演示片段",
  "ProjectName": "default"
}
```

- `GroupId` 必须是本平台逻辑组 Id（`group-na-` 前缀）。
- `URL` 必须是 http/https 地址，≤ 8192 字节，不允许内嵌凭据。
  - 指向本网关上传接口返回的地址（`/api/asset/files/...`）时直接复用已存储的文件。
  - 指向其他外部地址时，网关会**先下载该文件并转存到网关存储**，再以网关自己的公网地址同步给上游渠道。若下载失败（如地址仅调用方内网可达、防盗链、已失效），返回 HTTP 400 `InvalidParameter.URL`，并提示改用 `POST /api/asset/upload` 上传本地文件。
  - 转存同样受文件大小与格式限制（见上文「文件限制」），且实际文件类型需与 `AssetType` 一致，否则返回 HTTP 400。
- `AssetType` 取值：`Image` / `Video` / `Audio`。
- `Name` ≤ 64 字符，可选。

结果：

```json
{ "Id": "asset-na-..." }
```

创建后立即触发向所有启用渠道的同步；个别渠道失败会自动进入后台重试，不影响接口返回。

### ListAssets

```json
{
  "PageNumber": 1,
  "PageSize": 20,
  "Filter": { "GroupIds": ["group-na-..."], "AssetType": "Video" }
}
```

素材条目字段：

| 字段 | 说明 |
|------|------|
| `Id` / `GroupId` / `AssetType` / `Name` | 基础信息 |
| `URL` | 素材访问地址（可预览） |
| `Status` | 聚合状态：`Processing` / `Active` / `Failed` |
| `Error` | 失败原因 `{Code, Message}` |
| `CreateTime` / `UpdateTime` / `LastInferenceTime` | 时间戳（RFC3339 UTC） |
| `Replication` | 分渠道同步明细（仅管理员令牌返回，见下节） |

### GetAsset / UpdateAsset / DeleteAsset

- `GetAsset`：`{ "Id": "asset-na-..." }`，会顺带触发一次状态刷新（懒刷新兜底）。
- `UpdateAsset`：`{ "Id": "...", "Name": "新名称" }`（仅名称可改）。
- `DeleteAsset`：`{ "Id": "..." }`，同时删除本地存储与上游副本；个别渠道删除失败会后台重试，结果中 `RetryScheduled` 标识。

## 真人人像认证

真人人像用于**已获授权的真人**素材。终端用户本人完成一次活体认证后，平台会为其接入一个专属的真人素材组，之后即可向该组上传本人素材并在生成任务中引用。

> 平台**不采集、不存储、不比对**任何人脸信息：认证在认证页面上完成，平台只保存认证结果及其对应的素材组。

完整链路：

```
① 发起认证 ──► ② 终端用户打开认证页面完成活体认证 ──► ③ 轮询到 verified 拿到 GroupId
                                                              │
                          ④ 向该组 CreateAsset 上传本人素材 ──► ⑤ 生成任务中 asset:// 引用
```

- **①～③ 每个用户只需做一次**：认证结果是可复用的授权，请保存 `GroupId` 并在后续请求中复用。
- **④ 是必需步骤**：认证只建立「该用户 → 该组」的归属关系，组内初始为空，素材需要另行上传。
- 认证会话本身有有效期，过期后需重新发起。

### CreateFaceVerification

```json
{
  "ReturnUrl": "https://your-app.example.com/face-result"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `ReturnUrl` | string | 否 | 认证结束后浏览器跳转的地址，须为 http/https，≤ 1024 字符，不允许内嵌凭据。**仅用于浏览器回跳，平台不会向该地址推送结果**；认证状态仍须通过 `GetFaceVerification` 查询。 |

结果：

```json
{
  "VerificationId": "fv_xxxxxxxxx",
  "Status": "waiting_user",
  "H5Url": "https://...",
  "ExpiresAt": 1783740000
}
```

- 请在有效期内让终端用户打开 `H5Url` 完成认证，**不要解析或改写该地址**。
- `ExpiresAt` 为认证会话的过期时间（Unix 秒）。会话过期后 `Status` 变为 `expired`，须重新发起。
- **每次调用都会发起一次全新的认证会话**，已完成的认证不会被复用。若目标用户已有真人组，请直接复用其 `GroupId`，避免让用户重复认证。

### GetFaceVerification

```json
{ "VerificationId": "fv_xxxxxxxxx" }
```

轮询直到到达终态：

| Status | 说明 |
|--------|------|
| `waiting_user` | 等待终端用户完成认证 |
| `callback_received` | 已收到认证结果，正在处理 |
| `resolving` | 正在获取素材组信息 |
| `verified` | **认证成功**，结果中返回 `GroupId` |
| `failed` | 认证失败，需重新发起 |
| `expired` | 会话过期，需重新发起 |

认证成功时结果：

```json
{
  "VerificationId": "fv_xxxxxxxxx",
  "Status": "verified",
  "GroupId": "group-na-...",
  "ExpiresAt": 1783740000
}
```

拿到 `GroupId` 后，用 `CreateAsset` 把**本人**素材上传到该组，再按「[在生成任务中引用素材](#在生成任务中引用素材)」引用。

### 两个常见误区

1. **认证通过 ≠ 组里已有素材**：认证只完成归属建立，组内初始为空，素材必须另行上传。
2. **上传接口返回成功 ≠ 素材可用**：上传后上游会核验素材与认证用户是否为同一人，核验结果异步返回。必须轮询 `GetAsset` 直到 `Status == "Active"` 再引用，详见「[同步状态说明](#同步状态说明)」。

### 归属与查询

- 认证会话与真人素材组都只属于发起认证的账号：其他账号查询一律返回 `404 NotFound.VerificationId` / `404 NotFound.GroupId`。
- 真人组与普通组会一起出现在 `ListAssetGroups` / `ListAssets` 结果中，可用 `Filter.GroupType` 筛选（`AIGC` 或 `LivenessFace`）；省略该字段则返回全部。

## 同步状态说明

素材创建后，系统将其复制到每个启用的上游渠道。各渠道副本状态独立推进：

**副本状态机**：

```
pending ──► processing ──► ready（同步成功）
                 └──────► failed（同步失败）
```

- **State**（复制动作结果）：`pending` / `processing` / `ready` / `failed`
- **UpstreamStatus**（上游异步处理）：`Processing` / `Active` / `Failed`

**状态刷新机制**（无需接入方轮询）：

1. 创建后系统自动轮询上游（60s 起退避，最长 1 小时），成功或失败即停。
2. 超过 1 小时仍未终态的，退化为「用户查询时懒刷新」——调 `ListAssets` / `GetAsset` 时自动拉取最新状态。

**判断素材是否可用**：素材级 `Status == "Active"` 即所有渠道就绪；单渠道详情看 `Replication.Channels`（管理员令牌）：

```json
"Replication": {
  "Status": "partial",
  "Ready": 1,
  "Processing": 1,
  "Failed": 0,
  "Total": 2,
  "Channels": [
    { "ChannelId": 2, "Name": "示例渠道A", "State": "ready",  "UpstreamStatus": "Active" },
    { "ChannelId": 3, "Name": "示例渠道B", "State": "processing", "UpstreamStatus": "Processing" }
  ]
}
```

`State=failed` 的渠道会带 `LastError` 字段说明失败原因。

### 真人素材（LivenessFace）的同步差异

- 上传素材时上游会核验素材与认证用户是否为同一人。**不匹配（或无人脸）的素材最终状态为 `Failed`，`Error.Code` 为 `FaceMismatch`**，而上传接口当时仍会返回成功（异步校验）。
- 这类失败素材会被平台**自动清理**，不会残留在素材组中；本地仍保留失败记录与原因，便于排查。
- 因此真人素材**必须**轮询 `GetAsset` 直到 `Status == "Active"` 再引用：未就绪的素材会让生成请求直接失败（见下节）。

## 在生成任务中引用素材

视频生成（doubao 兼容渠道）等接口中，凡接受素材引用的位置，均可使用本地素材 Id 引用：

```
asset://asset-na-<32位hex>
```

示例（视频生成请求，注意：`asset://` 引用**仅在原生端点** `POST /api/v3/contents/generations/tasks` 支持，详见 [视频生成 API 文档](video-generation-api.md)）：

```json
{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {"type": "image_url", "image_url": {"url": "asset://asset-na-0f3a..."}},
    {"type": "audio_url", "audio_url": {"url": "asset://asset-na-9c2b..."}},
    {"type": "text", "text": "以参考图片为基础生成广告片"}
  ]
}
```

> 注意：OpenAI 风格端点（如 `POST /v1/video/generations`）**不支持** `asset://` 引用，请求会被拒绝并提示 `asset references are only supported by the native asset-library endpoint`。字段名大小写敏感：原生端点要求 `model`（小写）、素材放在 `content` 数组、提示词放在 `content[].text`。

网关转发前会自动把 `asset://` 引用改写为当前渠道对应的上游素材 Id，**前提是该素材在该渠道的副本状态为 ready**。未同步完成就引用会请求失败，请先确认 `Status`。

推荐做法：创建素材后轮询 `GetAsset`，直到 `Status` 变为 `Active`（一般几秒到 2 分钟）再发起生成请求。

引用不可用的素材时，生成请求会被直接拒绝：

| HTTP | 返回消息 | 原因 |
|------|----------|------|
| 403 | `Asset does not exist or does not belong to the current account` | 素材不存在，或不属于当前账号 |
| 503 | `No channel has replicas for all referenced assets` | 素材尚未就绪（`Processing` / `Failed`），或当前无可用渠道 |

出现 503 时请先查询该素材的 `Status` 与 `Error`，而不是直接重试请求。

## 错误码

| HTTP | Code | 说明 |
|------|------|------|
| 400 | `InvalidParameter.Version` | Version 不是 2024-01-01 |
| 400 | `InvalidParameter.Action` | 不支持的 Action |
| 400 | `InvalidParameter.*` | 各字段校验失败（Name/URL/GroupId/AssetType/PageNumber/PageSize/SortBy/ReturnUrl/VerificationId 等） |
| 400 | `InvalidParameter.GroupType` | `GroupType` 取值非法：仅支持 `AIGC`，真人素材组须走认证流程 |
| 400 | `InvalidRequestBody` | 请求体不是合法 JSON |
| 401 | `Unauthorized` | 令牌缺失或无效 |
| 404 | `NotFound.GroupId` / `NotFound.AssetId` | 组或素材不存在（或不属于当前用户） |
| 404 | `NotFound.VerificationId` | 认证会话不存在（或不属于当前用户） |
| 409 | `AssetGroupNotEmpty` | 删除组时组内仍有素材 |
| 413 | — | 上传文件超过大小限制（上传接口，`success:false` 信封） |
| 503 | `AssetLibraryUnavailable` | 管理员未启用任何素材库渠道 |
| 500 | `InternalError` | 服务端内部错误，凭 RequestId 排查 |
| 502 | `UpstreamError` | 认证上游返回错误，可稍后重试 |

上传接口（`/api/asset/upload`）不使用 Action 信封，错误直接返回 `{ "success": false, "message": "中文错误描述" }`。

## 端到端接入示例

```bash
BASE=https://your-gateway.example.com
TOKEN=sk-xxxxxxxx

# 1. 创建素材组
GROUP_ID=$(curl -sS -X POST "$BASE/api/asset-library?Action=CreateAssetGroup&Version=2024-01-01" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"Name":"营销素材"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["Result"]["Id"])')

# 2. 上传视频文件
ASSET_URL=$(curl -sS -X POST "$BASE/api/asset/upload" \
  -H "Authorization: Bearer $TOKEN" -F "file=@demo.mp4" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["Url"])')

# 3. 创建素材（自动同步上游）
ASSET_ID=$(curl -sS -X POST "$BASE/api/asset-library?Action=CreateAsset&Version=2024-01-01" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"GroupId\":\"$GROUP_ID\",\"URL\":\"$ASSET_URL\",\"AssetType\":\"Video\",\"Name\":\"demo\"}" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["Result"]["Id"])')

# 4. 轮询状态直到 Active
while :; do
  STATUS=$(curl -sS -X POST "$BASE/api/asset-library?Action=GetAsset&Version=2024-01-01" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d "{\"Id\":\"$ASSET_ID\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Result"]["Status"])')
  echo "status: $STATUS"
  [ "$STATUS" = "Active" ] && break
  [ "$STATUS" = "Failed" ] && break
  sleep 10
done

# 5. 在生成任务中引用（asset:// 仅原生端点支持）
curl -sS -X POST "$BASE/api/v3/contents/generations/tasks" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"model\":\"doubao-seedance-2-0-260128\",\"content\":[{\"type\":\"image_url\",\"image_url\":{\"url\":\"asset://$ASSET_ID\"}},{\"type\":\"text\",\"text\":\"让画面动起来\"}]}"
```

### 真人人像场景

在普通流程之前，先完成一次真人认证拿到真人素材组：

```bash
# 0. 发起认证，把 H5Url 交给终端用户打开
VERIFY=$(curl -sS -X POST "$BASE/api/asset-library?Action=CreateFaceVerification&Version=2024-01-01" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"ReturnUrl":"https://your-app.example.com/face-result"}')
VID=$(echo "$VERIFY" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Result"]["VerificationId"])')
echo "请让用户打开：$(echo "$VERIFY" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Result"]["H5Url"])')"

# 1. 轮询认证结果
while :; do
  RESULT=$(curl -sS -X POST "$BASE/api/asset-library?Action=GetFaceVerification&Version=2024-01-01" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d "{\"VerificationId\":\"$VID\"}")
  STATUS=$(echo "$RESULT" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Result"]["Status"])')
  echo "status: $STATUS"
  [ "$STATUS" = "verified" ] && break
  case "$STATUS" in failed|expired) echo "认证未通过：$STATUS"; exit 1;; esac
  sleep 5
done
GROUP_ID=$(echo "$RESULT" | python3 -c 'import sys,json;print(json.load(sys.stdin)["Result"]["GroupId"])')

# 2. 之后把本人素材 CreateAsset 到 $GROUP_ID，轮询到 Active 再引用
#    （步骤与上面的「创建素材 → 轮询状态 → 引用」完全一致）
```

拿到 `GROUP_ID` 后，后续步骤与普通素材完全相同 —— 区别只在于：这一步用的是真人组，且**必须**等到 `Status == "Active"` 才能引用。

## 附：与上游协议的关系

本素材库 API 与火山方舟 Assets API（Action 风格、`ResponseMetadata`+`Result` 信封、PascalCase 字段）保持 1:1 兼容，区别仅在：

- 入口为 `{$BASE}/api/asset-library`，鉴权用平台令牌（无需自行实现 AK/SK 签名）。
- 素材文件可先上传到平台（`/api/asset/upload`）再以 URL 创建，平台负责持久化存储与多渠道分发。
- `asset://` 引用会被平台自动改写为对应渠道的上游素材 Id，多渠道切换对调用方透明。
- 真人人像认证（`CreateFaceVerification` / `GetFaceVerification`）由平台统一封装：调用方无需接触认证页面的地址构造与状态解析，只需轮询平台返回的会话状态即可。

# 素材库 API 接入文档

面向下游接入方（应用开发者 / 企业内部系统）。素材库用于管理图片、视频、音频素材，并将其自动同步到已配置的上游素材库渠道（火山方舟 / 筷子科技 Assets API 兼容渠道），随后在视频生成等任务中以 `asset://` 引用使用。

## 目录

- [核心概念](#核心概念)
- [接入准备](#接入准备)
- [统一接口约定](#统一接口约定)
- [上传素材文件](#上传素材文件)
- [素材组管理](#素材组管理)
- [素材管理](#素材管理)
- [同步状态说明](#同步状态说明)
- [在生成任务中引用素材](#在生成任务中引用素材)
- [错误码](#错误码)
- [端到端接入示例](#端到端接入示例)

## 核心概念

| 概念 | 说明 |
|------|------|
| **素材组（Asset Group）** | 素材的容器，Id 形如 `group-na-<32位hex>`。创建素材时必须归属某个组。 |
| **素材（Asset）** | 一条图片/视频/音频记录，Id 形如 `asset-na-<32位hex>`。素材通过一个公网 URL 创建（通常是先调上传接口拿到的 URL）。 |
| **副本（Replica）** | 素材在上游渠道的一份同步记录。一个素材在每个启用的渠道各有一个副本，状态相互独立。 |
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

- `Name` 必填，≤ 64 字符；`Description` ≤ 300 字符；`GroupType` 默认 `AIGC`，≤ 32 字符；`ProjectName` 默认 `default`。
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
- `URL` 必须是公网可达的 http/https 地址，≤ 8192 字节，不允许内嵌凭据。
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
    { "ChannelId": 2, "Name": "筷子科技", "State": "ready",  "UpstreamStatus": "Active" },
    { "ChannelId": 3, "Name": "火山方舟", "State": "processing", "UpstreamStatus": "Processing" }
  ]
}
```

`State=failed` 的渠道会带 `LastError` 字段说明失败原因。

## 在生成任务中引用素材

视频生成（doubao 兼容渠道）等接口中，凡接受素材引用的位置，均可使用本地素材 Id 引用：

```
asset://asset-na-<32位hex>
```

示例（视频生成请求片段）：

```json
{
  "Model": "doubao-seedance",
  "Prompt": "以参考图片为基础生成广告片",
  "Image": "asset://asset-na-0f3a...",
  "Audio": "asset://asset-na-9c2b..."
}
```

网关转发前会自动把 `asset://` 引用改写为当前渠道对应的上游素材 Id，**前提是该素材在该渠道的副本状态为 ready**。未同步完成就引用会请求失败，请先确认 `Status`。

推荐做法：创建素材后轮询 `GetAsset`，直到 `Status` 变为 `Active`（一般几秒到 2 分钟）再发起生成请求。

## 错误码

| HTTP | Code | 说明 |
|------|------|------|
| 400 | `InvalidParameter.Version` | Version 不是 2024-01-01 |
| 400 | `InvalidParameter.Action` | 不支持的 Action |
| 400 | `InvalidParameter.*` | 各字段校验失败（Name/URL/GroupId/AssetType/PageNumber/PageSize/SortBy 等） |
| 400 | `InvalidRequestBody` | 请求体不是合法 JSON |
| 401 | `Unauthorized` | 令牌缺失或无效 |
| 404 | `NotFound.GroupId` / `NotFound.AssetId` | 组或素材不存在（或不属于当前用户） |
| 409 | `AssetGroupNotEmpty` | 删除组时组内仍有素材 |
| 413 | — | 上传文件超过大小限制（上传接口，`success:false` 信封） |
| 503 | `AssetLibraryUnavailable` | 管理员未启用任何素材库渠道 |
| 500 | `InternalError` | 服务端内部错误，凭 RequestId 排查 |

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

# 5. 在生成任务中引用
curl -sS -X POST "$BASE/v1/video/generations" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"Model\":\"doubao-seedance\",\"Prompt\":\"...\",\"Image\":\"asset://$ASSET_ID\"}"
```

## 附：与上游协议的关系

本素材库 API 与火山方舟 / 筷子科技 Assets API（Action 风格、`ResponseMetadata`+`Result` 信封、PascalCase 字段）保持 1:1 兼容，区别仅在：

- 入口为 `{$BASE}/api/asset-library`，鉴权用平台令牌（无需自行实现 AK/SK 签名）。
- 素材文件可先上传到平台（`/api/asset/upload`）再以 URL 创建，平台负责持久化存储与多渠道分发。
- `asset://` 引用会被平台自动改写为对应渠道的上游素材 Id，多渠道切换对调用方透明。

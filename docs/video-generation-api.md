# 视频生成 API 接入文档

面向下游接入方（应用开发者 / 企业内部系统）。本文档覆盖**视频生成任务**的创建、查询、素材引用与产物下载，适用于 Doubao Seedance 系列（火山方舟兼容渠道）。

如需在生成任务中引用素材库里的图片 / 视频 / 音频（`asset://` 引用），请先阅读 [素材库 API 接入文档](asset-library-api.md)。

## 目录

- [接入准备](#接入准备)
- [端点总览](#端点总览)
- [原生端点（推荐，支持 asset://）](#原生端点推荐支持-asset)
- [查询任务](#查询任务)
- [在请求中引用素材（asset://）](#在请求中引用素材asset)
- [OpenAI 风格端点](#openai-风格端点)
- [产物下载](#产物下载)
- [常见错误](#常见错误)
- [端到端示例](#端到端示例)

## 接入准备

| 项目 | 说明 |
|------|------|
| Base URL | 平台网关地址，如 `https://your-gateway.example.com` |
| 鉴权 | `Authorization: Bearer <平台令牌>`（sk- 开头） |
| 模型名 | 如 `doubao-seedance-2-0-260128`，可通过 `GET /v1/models` 查询可用列表 |

## 端点总览

| 方法 | 路径 | 说明 | asset:// |
|------|------|------|----------|
| POST | `/api/v3/contents/generations/tasks` | 创建生成任务（原生格式，**推荐**） | ✅ 支持 |
| GET | `/api/v3/contents/generations/tasks/{task_id}` | 查询任务状态与结果 | — |
| POST | `/v1/video/generations` | 创建生成任务（OpenAI 风格统一格式） | ❌ 不支持 |
| GET | `/v1/video/generations/{task_id}` | 查询任务（同上，返回格式一致） | — |
| GET | `/v1/videos/{task_id}/content` | 视频内容代理下载（令牌或会话鉴权） | — |

> 素材引用只在**原生端点**可用。OpenAI 风格端点收到 `asset://` 会直接拒绝（见 [常见错误](#常见错误)）。

## 原生端点（推荐，支持 asset://）

### 创建任务

```bash
POST /api/v3/contents/generations/tasks
Content-Type: application/json
Authorization: Bearer <TOKEN>
```

请求体：

```json
{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {"type": "text", "text": "镜头缓缓推进，画面中的人物微微转头微笑"},
    {"type": "image_url", "image_url": {"url": "asset://asset-na-<32位hex>"}}
  ]
}
```

**关键字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 模型名（**小写**，区分大小写） |
| `content` | array | 是 | 内容数组，元素顺序不限 |
| `content[].type=text` | — | 是（至少一个） | 提示词，放在 `text` 字段 |
| `content[].type=image_url` | — | 否 | 参考图片，URL 放 `image_url.url`，支持 `asset://` 素材引用或公网 HTTPS URL |
| `content[].type=video_url` | — | 否 | 参考视频，同上 |
| `content[].type=audio_url` | — | 否 | 参考音频，同上 |

**可选生成参数**（与原生 API 同名直传）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `duration` | int | 时长（秒），默认 5 |
| `resolution` | string | 分辨率，如 `720p`、`1080p` |
| `ratio` | string | 画面比例，如 `1:1`、`16:9`、`9:16` |
| `seed` | int | 随机种子，用于复现 |
| `camera_fixed` | bool | 是否固定镜头 |
| `watermark` | bool | 是否加水印 |
| `generate_audio` | bool | 是否生成音频 |
| `return_last_frame` | bool | 是否返回最后一帧 |
| `callback_url` | string | 任务完成回调地址 |

成功响应（HTTP 200）：

```json
{"id": "task_cdOKNR9D0PUPbRccXeZDcglZF8VvPrA2"}
```

> **易错点**：字段名**全小写、区分大小写**。`Model`/`Prompt`/`Image` 等大写开头字段不会被识别，会导致上游收到空模型名而报错。

## 查询任务

```bash
GET /api/v3/contents/generations/tasks/{task_id}
Authorization: Bearer <TOKEN>
```

真实响应示例（任务完成后）：

```json
{
  "id": "task_cdOKNR9D0PUPbRccXeZDcglZF8VvPrA2",
  "model": "doubao-seedance-2-0-260128",
  "status": "succeeded",
  "created_at": 1787382092,
  "updated_at": 1787382290,
  "content": {
    "video_url": "https://ark-acg-cn-beijing.tos-cn-beijing.volces.com/....mp4?X-Tos-Algorithm=..."
  },
  "seed": 31445,
  "resolution": "720p",
  "ratio": "1:1",
  "duration": 5,
  "framespersecond": 24,
  "generate_audio": true,
  "usage": {"completion_tokens": 108900, "total_tokens": 108900}
}
```

**状态机：**

```
queued ──► running ──► succeeded
                    └─► failed（失败原因见 error.message）
```

| status | 含义 |
|--------|------|
| `queued` | 排队中 |
| `running` | 生成中 |
| `succeeded` | 成功，结果在 `content.video_url` |
| `failed` | 失败 |

**轮询建议**：间隔 10 秒，一般 1~3 分钟完成。也可以在创建时传 `callback_url` 改为回调通知。

**注意**：`content.video_url` 是**带签名的临时链接（约 24 小时有效）**，请在有效期内下载转存，不要持久化到业务库。

## 在请求中引用素材（asset://）

素材库中状态为 `Active` 的素材，可在原生端点任意 `*_url` 位置引用：

```
asset://asset-na-<32位hex>
```

平台在转发前会自动完成两件事：

1. **渠道路由**：只路由到「所有被引用素材都已同步 ready」的渠道；没有满足条件的渠道返回 503。
2. **引用改写**：把 `asset://` 本地素材 Id 改写为当前渠道对应的上游素材 Id，对调用方完全透明。

完整素材上传 / 同步 / 状态查询流程见 [素材库 API 接入文档](asset-library-api.md)。

## OpenAI 风格端点

```bash
POST /v1/video/generations
Content-Type: application/json
Authorization: Bearer <TOKEN>
```

统一格式（同样全小写）：

```json
{
  "model": "doubao-seedance-2-0-260128",
  "prompt": "镜头缓缓推进，画面中的人物微微转头微笑",
  "image": "https://example.com/ref.jpg"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `model` | string | 模型名（必填） |
| `prompt` | string | 提示词（必填） |
| `image` | string | 单张参考图 URL（可选） |
| `images` | []string | 多张参考图 URL（可选） |
| `duration` | int | 时长秒数（可选） |

任务查询：`GET /v1/video/generations/{task_id}`，响应格式与原生端点一致。

> **限制**：该端点**不支持** `asset://` 引用。如需使用素材库素材，请改用原生端点 `/api/v3/contents/generations/tasks`。

## 产物下载

除直接下载 `content.video_url` 外，还可走平台代理（免去处理签名过期）：

```bash
GET /v1/videos/{task_id}/content
Authorization: Bearer <TOKEN>
```

- 返回视频二进制流，可直接 `curl -o out.mp4` 或作为 `<video>` 源
- 任务未完成时返回 400 并提示当前状态
- 支持平台令牌（API 客户端）与会话 Cookie（控制台页面）两种鉴权

## 常见错误

以下错误均为实测，可直接对照排查：

| HTTP | 错误信息 | 原因与解决 |
|------|----------|------------|
| 401 | `Invalid token` | 令牌缺失 / 无效 / 已删除 |
| 400 | `Invalid asset URI; use an account asset ID` | `asset://` 后不是合法的本地素材 Id 格式 |
| 400 | `prompt is required` | 原生格式缺少 `content[].type=text` 的提示词 |
| 400 | 上游返回 `Model name not specified` | 字段名大写了（`Model`→`model`），或模型名缺失 |
| 403 | `Asset does not exist or does not belong to the current account` | 素材不存在或属于其他账号 |
| 400 | `asset references are only supported by the native asset-library endpoint` | 在 `/v1/video/generations` 等非原生端点使用了 `asset://`，改用原生端点 |
| 503 | `No channel has replicas for all referenced assets` | 被引用素材尚未在任何渠道同步 ready，先轮询 `GetAsset` 等 `Active` |
| 429 | 速率限制 | 降低并发或联系管理员 |

## 端到端示例

### A. 文生视频（无素材）

```bash
BASE=https://your-gateway.example.com
TOKEN=sk-xxxxxxxx

# 1. 创建任务
TASK_ID=$(curl -sS -X POST "$BASE/api/v3/contents/generations/tasks" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{
    "model": "doubao-seedance-2-0-260128",
    "content": [
      {"type": "text", "text": "夕阳下的城市延时摄影，车流如织"}
    ],
    "duration": 5,
    "resolution": "720p",
    "ratio": "16:9"
  }' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')

# 2. 轮询直到完成
while :; do
  RESP=$(curl -sS "$BASE/api/v3/contents/generations/tasks/$TASK_ID" \
    -H "Authorization: Bearer $TOKEN")
  STATUS=$(echo "$RESP" | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])')
  echo "status: $STATUS"
  case "$STATUS" in succeeded|failed) break;; esac
  sleep 10
done

# 3. 下载视频（代理方式，免签名过期问题）
curl -sS -o result.mp4 "$BASE/v1/videos/$TASK_ID/content" \
  -H "Authorization: Bearer $TOKEN"
```

### B. 用素材库图片生成视频（图生视频）

前置步骤（上传素材并等 `Active`）见 [素材库 API 接入文档](asset-library-api.md) 的端到端示例。

```bash
ASSET_ID=asset-na-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

curl -sS -X POST "$BASE/api/v3/contents/generations/tasks" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{
    \"model\": \"doubao-seedance-2-0-260128\",
    \"content\": [
      {\"type\": \"image_url\", \"image_url\": {\"url\": \"asset://$ASSET_ID\"}},
      {\"type\": \"text\", \"text\": \"镜头缓缓推进，画面中的人物微微转头微笑\"}
    ]
  }"
```

查询与下载同示例 A。

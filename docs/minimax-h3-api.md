# MiniMax-H3 系列 API 接入文档

面向下游接入方（应用开发者 / 企业内部系统）。本文档覆盖 **MiniMax-H3 系列文生视频 / 图生视频 / 多参参考视频任务**的创建、查询与计费规则。该系列经平台网关（new-api sora 任务通道）转发至 maitoken 上游，素材字段采用 **MiniMax 原生文档风格**，网关原样透传并同步完成素材计费识别。

## 目录

- [接入准备](#接入准备)
- [端点总览](#端点总览)
- [创建任务](#创建任务)
- [素材语义与场景](#素材语义与场景)
- [场景示例](#场景示例)
- [查询任务与产物](#查询任务与产物)
- [计费规则](#计费规则)
- [常见错误](#常见错误)
- [端到端示例](#端到端示例)

## 接入准备

| 项目 | 说明 |
|------|------|
| Base URL | 平台网关地址（本地联调 `http://127.0.0.1:3000`；正式环境以实际部署网关为准） |
| 鉴权 | `Authorization: Bearer <平台令牌>`（sk- 开头） |
| 请求格式 | **仅支持 JSON**（`Content-Type: application/json`）；multipart/form-data 上传会被上游拒绝 |
| 模型名 | 见下表，**大小写敏感**，必须精确匹配 |

### 可用模型（4 个）

| 模型名（精确） | 支持分辨率 | 说明 |
|------|------|------|
| `MiniMax-H3` | 768P / 2K | 旗舰，支持文生 / 首尾帧 / 多参参考 |
| `minimax-h3-base` | 768P | 基础版 |
| `minimax-h3-base-fast` | 768P | 快速版（注意是 base-fast，非 fast） |
| `minimax-h3-mini` | 768P | Mini 版 |

> ⚠️ 模型名大小写敏感：`minimax-h3`（全小写）会被上游拒绝（404 model_not_found）；`MiniMax-H3` 的 H 必须大写。

## 端点总览

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/videos` | 创建视频生成任务 |
| GET | `/v1/videos/{task_id}` | 查询任务状态与结果（`task_id` 为创建时返回的公开 ID） |

任务为**异步**：创建后立即返回排队中的任务，需轮询查询接口直到 `completed` / `failed`。

## 创建任务

```http
POST /v1/videos
Content-Type: application/json
Authorization: Bearer <TOKEN>
```

### 请求参数

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | ✅ | 上表 4 个模型名之一，大小写敏感 |
| `prompt` | string | ✅ | 画面描述 |
| `duration` | int | 建议 | 生成秒数 **4~15**（上游缺省 5；未传时网关按 4 秒预扣，建议显式传） |
| `size` | string | 可选 | 输出分辨率（同时决定**计费档位**）：`720x1280`=768P、`1440x2560`=2K；横屏可传 `1280x720` / `2560x1440`。按**长边**判定：长边 ≥1792 计 2K，其余计 768P。缺省 `720x1280` |
| `aspect_ratio` | string | 可选 | 画面比例（如 `9:16` / `16:9` / `adaptive`），透传上游 |
| `image` | string | 条件 | 首帧图片 URL（图生视频 / 首尾帧） |
| `last_frame_image_url` | string | 条件 | 尾帧图片 URL（与 `image` 同传即首尾帧） |
| `reference_image_urls` | string[] | 条件 | 参考图，最多 **9** 张 |
| `reference_video_urls` | string[] | 条件 | 参考视频，最多 **3** 段（按实际时长计费，见[计费规则](#计费规则)） |
| `reference_audio_urls` | string[] | 可选 | 参考音频，最多 **3** 段（免费，不计费） |

> 素材 URL 必须为**中国大陆可公网访问**的地址（境外直链不可用），或直接传 Base64。

## 素材语义与场景

| 场景 | 传参方式 |
|------|------|
| 文生视频（T2V） | 仅 `prompt`（可加 `duration` / `size`） |
| 首帧生视频（图生视频） | `image`（单张 URL / Base64） |
| 首尾帧 | `image` + `last_frame_image_url` |
| 多参参考（图 / 视频 / 音频） | `reference_image_urls`（≤9） / `reference_video_urls`（≤3） / `reference_audio_urls`（≤3），可组合 |
| 混合 | 首帧 + 参考图/视频可同时使用，均计入素材计费 |

## 场景示例

### 文生视频

```json
{
  "model": "MiniMax-H3",
  "prompt": "让画面中的人物向镜头挥手，然后转身走向远处",
  "aspect_ratio": "9:16",
  "size": "720x1280",
  "duration": 5
}
```

### 首帧生视频

```json
{
  "model": "MiniMax-H3",
  "prompt": "让画面中的人物向镜头挥手，然后转身走向远处",
  "image": "https://example.com/first.png",
  "size": "720x1280",
  "duration": 5
}
```

### 首尾帧

```json
{
  "model": "MiniMax-H3",
  "prompt": "从首帧自然过渡到尾帧，镜头平滑推进",
  "image": "https://example.com/first.png",
  "last_frame_image_url": "https://example.com/last.png",
  "aspect_ratio": "adaptive",
  "size": "720x1280",
  "duration": 5
}
```

### 多参参考（参考图 + 参考视频）

```json
{
  "model": "MiniMax-H3",
  "prompt": "参考视频中的主体动作，生成一段连续自然的画面",
  "reference_image_urls": ["https://example.com/character.png"],
  "reference_video_urls": ["https://example.com/ref-clip.mp4"],
  "size": "1440x2560",
  "duration": 4
}
```

> 实测：`reference_video_urls` 传 10s 真实视频 + 4s 输出 768P，任务约 4~5 分钟完成出片，产物在 MiniMax 官方 CDN。

## 查询任务与产物

```http
GET /v1/videos/{task_id}
Authorization: Bearer <TOKEN>
```

### 状态枚举

| status | 含义 |
|--------|------|
| `queued` / `unknown` | 排队中（创建早期的 `unknown` 属正常，继续轮询） |
| `processing` / `in_progress` | 生成中（`progress` 0~100） |
| `completed` | 完成，产物见 `metadata.url` |
| `failed` | 失败（`error.message` 为原因） |

### 响应示例（completed）

以下示例为「参考视频输入 3s + 输出 5s 768P」场景，响应中含本网关新增的消耗金额字段：

```json
{
  "id": "task_UHCrH3MSUSZNViWf80t8jCntipXG9AO0",
  "task_id": "task_fD6VYRmpxLaiTUp8CBFYCVWvdDF5MK3D",
  "object": "video",
  "model": "minimax-h3",
  "status": "completed",
  "progress": 100,
  "created_at": 1788333269,
  "completed_at": 1788333573,
  "consumed_input_quota": 110619,
  "consumed_input_amount": 1.499994,
  "consumed_output_quota": 184366,
  "consumed_output_amount": 2.500003,
  "metadata": {
    "url": "https://video-product.cdn.minimax.io/inference_output/rollout/2026-09-02/xxxx/output.mp4"
  }
}
```

**视频产物下载地址在 `metadata.url`**（MiniMax 官方 CDN，链接带有效期，请及时下载）。

### 消耗金额字段（consumed_*）

查询接口（GET `/v1/videos/{task_id}`）对 **MiniMax-H3 系列任务**额外返回 4 个顶层字段，供下游**记录与对账**使用：

| 字段 | 类型 | 含义 |
|------|------|------|
| `consumed_input_quota` | int | 输入消耗 quota（视频输入实际时长 + 超额图片折算，拆分口径见[计费规则](#计费规则)） |
| `consumed_input_amount` | number | 输入消耗金额（人民币，**已含渠道分组折扣**） |
| `consumed_output_quota` | int | 输出消耗 quota（输出秒数 × 分辨率倍率） |
| `consumed_output_amount` | number | 输出消耗金额（人民币，**已含渠道分组折扣**） |

要点：

1. **仅查询接口返回**；创建接口（POST `/v1/videos`）与生成中的任务不含本组字段。
2. **守恒**：`consumed_input_quota + consumed_output_quota` 恒等于任务总扣费 quota（拆分无 ±1 误差），可直接与后台账单/流水核对。
3. 金额换算：`amount = quota ÷ 500000 × 站点汇率`（人民币，保留 6 位小数）；quota 与 amount 是同一笔扣费的两套口径，任选其一记录即可。
4. 金额为**任务预扣的最终扣费**（任务失败也不退费，与下方计费规则一致）；纯文生（T2V）任务输入为 0、全部归输出。
5. **兼容存量**：本组字段上线前创建的旧任务无拆分信息，查询时自动全归输出（输入 0）。
6. 仅 MiniMax-H3 系列返回本组字段；其他模型（doubao-seedance 等）的查询响应不受影响、不返回。

## 计费规则

平台按**输出秒数 × 分辨率单价 + 素材费**折算后扣费。基准价：768P 每秒 ¥0.5，四个模型同价。

### 价格表

| 分辨率 | size 写法（示例） | 单价 | 倍率 |
|--------|------|------|------|
| 768P | `720x1280` | ¥0.50 / 秒 | 1.0 |
| 2K | `1440x2560` | ¥0.80 / 秒 | 1.6 |

### 素材规则

| 素材 | 计费规则 |
|------|------|
| 图片（首帧 `image` / 尾帧 `last_frame_image_url` / 参考图 `reference_image_urls`） | **≤5 张免费**；超出部分每张 ¥0.20 |
| 视频输入（`reference_video_urls` / `input_reference` 视频 URL） | 按**实际输入时长** × 输出分辨率单价计费（网关远程解析真实时长） |
| 音频（`reference_audio_urls`） | 免费 |

### 费用公式

```
等效秒数 = (输出秒数 + Σ 视频输入实际时长) × 分辨率倍率 + max(0, 图片张数 − 5) × 0.4
费用 = ¥0.5 × 等效秒数 × 渠道分组折扣
```

- 倍率：768P = 1.0、2K = 1.6
- 视频输入时长解析失败（非 MP4/MOV、网络异常等）时，该段按「输入时长 = 输出秒数」近似
- 渠道分组折扣：由平台后台对该渠道配置，默认 1.0

#### 输入 / 输出拆分口径

查询接口的 `consumed_input_*` / `consumed_output_*` 按「等效基准秒数」占比拆分上面的总扣费：

- **输出消耗** = 输出秒数 × 分辨率倍率
- **输入消耗** = Σ视频输入实际时长 × 分辨率倍率 + max(0, 图片张数 − 5) × 0.4

其中输入 quota 按占比四舍五入取整、输出 quota 取余补齐，保证 `input + output == 总扣费` 恒成立。

### 实测锚点

| 场景 | 扣费 |
|------|------|
| 纯文生 4s 768P | ¥2.00 |
| 纯文生 4s 2K | ¥3.20 |
| 参考视频输入 10s + 输出 4s 768P | ¥7.00（等效 14s） |
| 多图（超额） | 每超 1 张 +¥0.20 |

### ⚠️ 注意

1. **任务预扣即最终扣费**（new-api 任务机制）：创建任务时即按上述规则预扣，任务**失败不退费**。提交前请确认素材 URL 可达、参数合法。
2. **计费档位看 `size` 字段**：若按 maitoken 文档习惯只传 `resolution: "2K"` 而不传 `size`，将按 768P（缺省 `720x1280`）计费——传 2K 输出时请务必同时传 `size: "1440x2560"`。
3. 视频参考素材 URL 需大陆可达，境外直链会导致素材下载失败（任务失败且不退费）。

## 常见错误

| 现象 | 原因 / 处理 |
|------|------|
| 404 `model_not_found` | 模型名大小写错误（如全小写 `minimax-h3`）；用精确名 `MiniMax-H3` |
| 400 `prompt is required` | 缺少 `prompt` |
| 502（multipart） | 上游只接受 JSON，勿用 multipart/form-data 上传 |
| failed：`media not found (HTTP 404)` | 素材 URL 不可达 / 境外直链；换大陆可达地址或 Base64 |
| failed：`UNSUPPORTED_INPUT` | 素材类型/字段不被该模型支持（如 base/fast/mini 不支持 2K） |
| 失败不退费 | 任务机制：预扣即最终，提交前先确认素材与参数 |

## 端到端示例

```bash
# 1. 创建任务
curl -X POST https://<gateway>/v1/videos \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "MiniMax-H3",
    "prompt": "参考视频中的主体动作，生成一段连续自然的画面",
    "reference_video_urls": ["https://example.com/ref-clip.mp4"],
    "size": "720x1280",
    "duration": 4
  }'
# → {"id":"task_xxxx","status":"queued",...}

# 2. 轮询状态（completed 后响应含 consumed_input/output_* 四字段）
curl https://<gateway>/v1/videos/task_xxxx -H "Authorization: Bearer sk-xxxx"
# → {"id":"task_xxxx","status":"completed",
#    "consumed_input_quota":0, "consumed_input_amount":0,
#    "consumed_output_quota":184365, "consumed_output_amount":2.499989, ...}

# 3. completed 后取产物
# metadata.url → https://video-product.cdn.minimax.io/.../output.mp4
```

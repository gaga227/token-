# 万相 Wan3.0 视频 API 接入文档

面向下游接入方（应用开发者 / 企业内部系统）。本文档覆盖 **阿里万相 3.0（wan3.0-video / wan3.0-video-prime）文生视频 / 图生视频 / 首尾帧 / 多参参考任务**的创建、查询与计费规则。

该系列经平台网关（new-api ali 任务通道）转发至上游（DashScope 原生协议），网关负责：统一 OpenAI 风格的请求/响应格式、参数映射、预扣费与**按上游实际用量的差额结算**。调用方无需接触阿里原生协议细节，需要精细控制时可通过 `metadata` 字段透传原生参数。

## 目录

- [接入准备](#接入准备)
- [端点总览](#端点总览)
- [创建任务](#创建任务)
- [参数映射规则](#参数映射规则)
- [场景示例](#场景示例)
- [metadata 透传（高级）](#metadata-透传高级)
- [查询任务与产物](#查询任务与产物)
- [计费规则](#计费规则)
- [常见错误](#常见错误)
- [端到端示例](#端到端示例)

## 接入准备

| 项目 | 说明 |
|------|------|
| Base URL | 平台网关地址（本地联调 `http://127.0.0.1:3000`；正式环境以实际部署网关为准） |
| 鉴权 | `Authorization: Bearer <平台令牌>`（sk- 开头） |
| 请求格式 | 仅支持 JSON（`Content-Type: application/json`） |
| 模型名 | 见下表，精确匹配 |

### 可用模型（2 个）

| 模型名 | 定位 | 说明 |
|------|------|------|
| `wan3.0-video` | 标准版 | 全模态参考（图/视频/音频/文件/链接），按**输出时长**计费 |
| `wan3.0-video-prime` | 高速版 | 能力对齐标准版，速度显著提升，价格 1.5 倍；带输入视频时按**输入时长**计费（见[计费规则](#计费规则)） |

## 端点总览

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/videos` | 创建视频生成任务 |
| GET | `/v1/videos/{task_id}` | 查询任务状态与结果（`task_id` 为创建时返回的公开 ID） |

任务为**异步**：创建后立即返回排队中的任务，需轮询查询接口直到 `completed` / `failed`（建议间隔 5~10 秒）。网关侧自动轮询上游并同步状态。

## 创建任务

```http
POST /v1/videos
Content-Type: application/json
Authorization: Bearer <TOKEN>
```

### 请求参数

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | ✅ | `wan3.0-video` / `wan3.0-video-prime` |
| `prompt` | string | ✅ | 画面描述，中英文均可，最长 20000 字符（超出自动截断）。参考模式下可用 "Image 1"、"Video 1" 等按顺序引用素材 |
| `seconds` | string | 建议 | 生成时长（秒），**2~30**；缺省按 5 秒计费。⚠️ 智能时长 `-1` 不能走本字段（网关校验 1~3600 会拒绝），须用 `metadata.parameters.duration` 透传 |
| `duration` | int | 可选 | 同 `seconds` 的整数写法，二选一 |
| `size` | string | 建议 | 分辨率 / 画幅（同时决定**计费档位**），三种写法见下表。缺省 `1080P`（上游默认），**计费按 1080P 档**，建议显式传递 |
| `image` | string | 条件 | 首帧图片 URL（图生视频 / 首尾帧） |
| `images` | string[] | 条件 | 图片数组：第 1 张→首帧，第 2 张→尾帧（首尾帧模式） |
| `input_reference` | string | 条件 | 首帧素材 URL（与 `image` 等效，优先级低于 `image` / `images`） |
| `metadata` | object | 可选 | 原生参数透传，见 [metadata 透传](#metadata-透传高级) |

> ⚠️ `reference_image_urls` / `reference_video_urls` / `reference_audio_urls` / `last_frame_image_url` 为 MiniMax 系列字段，**wan3.0 不生效**。多参参考素材请用 `metadata.input.media`（见高级用法）。

### `size` 三种合法写法

| 写法 | 示例 | 映射为 |
|------|------|------|
| 分辨率档位（推荐） | `480P` / `720P` / `1080P` | `parameters.resolution` |
| 宽高比 | `16:9` / `9:16` / `4:3` / `3:4` / `1:1` / `adaptive` | `parameters.ratio` |
| 像素尺寸（星号分隔） | `1920*1080` | 换算为 `parameters.resolution` |

> ⚠️ **不要传 `1280x720`（小写 x）**——既非星号尺寸也非档位，会被当成分辨率字符串拼成非法值发往上游，报 `InvalidParameter`。

分辨率档位与画幅比例可组合：`size` 传档位、画幅走 `metadata.parameters.ratio`（或反之）。

### 素材输入约束（上游限制）

| 素材类型 | media type | 数量 / 规格 |
|------|------|------|
| 首帧图 | `first_frame` | ≤1 张；JPEG/JPG/PNG/BMP/WEBP，边长 [240,8000]，宽高比 ≤8:1，≤20MB |
| 尾帧图 | `last_frame` | ≤1 张；同上 |
| 参考图 | `reference_image` | ≤10 张；同上 |
| 参考视频 | `reference_video` | ≤5 段；mp4/mov，每段 [1,15] 秒且总时长 ≤15 秒，≥16fps，单段 ≤100MB |
| 参考音频 | `reference_audio` | ≤5 段；wav/mp3，每段 [1,15] 秒且总时长 ≤15 秒，≤15MB |
| 文件 | `file` | ≤1 个；docx/doc/xlsx/xls/pptx/ppt/pdf/txt/md 等，≤100MB、≤50 页 |
| 网页链接 | `link` | ≤1 个；免登录公开网页 |

**组合规则**：`first_frame`/`last_frame` 与 `reference_*`/`file`/`link` **互斥**（同请求不可混用）；`reference_image`/`reference_video`/`reference_audio` 三者可自由组合；`file` 与 `link` 互斥。素材 URL 需公网可达（上游服务器需能下载）。

## 参数映射规则

网关将上述请求字段映射为阿里原生协议后发往上游：

```
model            → model
prompt           → input.prompt
image / images[0] / input_reference → input.media[] {type: "first_frame", url}
images[1]        → input.media[] {type: "last_frame", url}
size: "480P" 等  → parameters.resolution
size: "16:9" 等  → parameters.ratio
size: "1920*1080" → 换算为 parameters.resolution
seconds / duration → parameters.duration
metadata.input.*    → input 原生字段（media / audio_url 等，直接覆盖）
metadata.parameters.* → parameters 原生字段（resolution / ratio / duration / seed / audio 等，直接覆盖）
```

> 网关固定项：`prompt_extend` 对 wan3.0 **不下发**（走上游默认）；`watermark` 不下发（上游默认 false，无水印）。

## 场景示例

### 文生视频

```json
{
  "model": "wan3.0-video",
  "prompt": "山间日出，清晨薄雾流动，电影运镜，镜头缓慢向前推进，写实摄影，8k画质",
  "seconds": "10",
  "size": "1080P"
}
```

### 首帧生视频（图生视频）

```json
{
  "model": "wan3.0-video",
  "prompt": "让画面中的人物向镜头挥手，然后转身走向远处",
  "image": "https://example.com/first.png",
  "seconds": "5",
  "size": "720P"
}
```

### 首尾帧

```json
{
  "model": "wan3.0-video",
  "prompt": "从首帧自然过渡到尾帧，镜头平滑推进",
  "images": ["https://example.com/first.png", "https://example.com/last.png"],
  "seconds": "5",
  "size": "720P"
}
```

### 多参参考（参考图 + 参考视频 + 参考音频，全模态）

```json
{
  "model": "wan3.0-video",
  "prompt": "保持 Image 1 中角色的外观特征，参考 Video 1 的运镜方式，生成一段新的画面",
  "seconds": "5",
  "size": "720P",
  "metadata": {
    "input": {
      "media": [
        {"type": "reference_image", "url": "https://example.com/character.png"},
        {"type": "reference_video", "url": "https://example.com/ref-clip.mp4"},
        {"type": "reference_audio", "url": "https://example.com/bgm.wav"}
      ]
    }
  }
}
```

### 视频编辑 / 视频延长（reference_video + 指令）

```json
{
  "model": "wan3.0-video",
  "prompt": "将整个画面转换为黏土艺术风格",
  "size": "720P",
  "metadata": {
    "input": {
      "media": [
        {"type": "reference_video", "url": "https://example.com/source.mp4"}
      ]
    },
    "parameters": {"ratio": "adaptive"}
  }
}
```

## metadata 透传（高级）

`metadata` 为对象，两个子对象分别映射到阿里原生协议的 `input` 与 `parameters`，字段与阿里百炼官方文档一致：

```json
{
  "metadata": {
    "input": {
      "media": [{"type": "reference_image", "url": "..."}],
      "audio_url": "https://example.com/audio.wav"
    },
    "parameters": {
      "resolution": "720P",
      "ratio": "16:9",
      "duration": -1,
      "seed": 42,
      "audio": true
    }
  }
}
```

常用场景：

| 需求 | 用法 |
|------|------|
| **智能时长**（-1，模型自动推荐 2~30 秒） | `metadata.parameters.duration: -1`（`seconds` 字段无法传负数） |
| 多参参考素材 | `metadata.input.media` 数组（阿里原生 media 格式） |
| 锁定随机种子复现结果 | `metadata.parameters.seed`（0~2147483647；同 seed 结果相近但不保证完全一致） |
| 画幅 + 分辨率同时指定 | `size: "720P"` + `metadata.parameters.ratio: "9:16"` |
| 关闭音频输出 | `metadata.parameters.audio: false`（不影响计费） |

> `metadata` 中的字段**优先级高于**顶层同义字段（如 `metadata.parameters.duration` 覆盖 `seconds`）。`model` 字段不允许通过 metadata 修改。

## 查询任务与产物

```http
GET /v1/videos/{task_id}
Authorization: Bearer <TOKEN>
```

### 状态枚举

| status | 含义 |
|--------|------|
| `queued` | 排队中 |
| `in_progress` | 生成中（`progress` 0~100） |
| `completed` | 完成，产物在 `metadata.url` |
| `failed` | 失败（`error.message` 为原因），**已自动全额退款** |

### 响应示例（completed）

```json
{
  "id": "task_M3FYmrkCOyAULvUaMPSTrOf1wpAkB5dH",
  "object": "video",
  "model": "wan3.0-video",
  "status": "completed",
  "progress": 100,
  "created_at": 1788531736,
  "completed_at": 1788532700,
  "metadata": {
    "url": "https://dashscope-xxxx.oss-accelerate.aliyuncs.com/.../video.mp4"
  }
}
```

**视频产物下载地址在 `metadata.url`**（阿里云 OSS 直链，有效期约 24 小时，请及时转存）。输出规格：MP4、30fps、默认带音频。

## 计费规则

任务创建时按请求参数**预扣**，任务完成后按上游返回的实际用量**差额结算**（多退少补），失败任务**全额退款**。

### 价格表（站点汇率 6.78，分组倍率 1）

| 分辨率 | wan3.0-video | wan3.0-video-prime |
|------|------|------|
| 480P | ¥0.075 / 秒 | ¥0.112 / 秒 |
| 720P | ¥0.150 / 秒 | ¥0.225 / 秒 |
| 1080P（缺省档） | ¥0.299 / 秒 | ¥0.449 / 秒 |

### 计费口径

| 模型 | 计费时长 |
|------|------|
| `wan3.0-video` | **实际输出时长**（上游 `usage.output_video_duration`） |
| `wan3.0-video-prime`（带输入视频） | **输入视频时长**（上游 `usage.input_video_duration`，对齐上游计费口径） |
| `wan3.0-video-prime`（纯文生/图生） | 实际输出时长 |

差额结算公式：

```
实际扣费 = 预扣费 × (实际计费秒数 × 实际分辨率系数) ÷ (预估秒数 × 预估分辨率系数)
```

- 分辨率系数：480P=1、720P=2、1080P=4；实际值按上游返回的 `usage.SR` 修正（如请求 720P 实际产出 480P 按 480P 结算）
- 智能时长（-1）任务按 30 秒预扣，完成后按实际时长结算，**多扣自动退还**

### 实测锚点

| 场景 | 扣费 |
|------|------|
| 纯文生 5s @720P | ¥0.749 |
| 纯文生 5s @480P | ¥0.374 |
| 智能时长（-1）预扣 30s @480P，实际出片 5s | 净扣 ¥0.374（退 ¥1.86） |
| 参数错误任务失败 | 全额退款 |

### ⚠️ 注意

1. **不传 `seconds` / `size` 时的计费**：缺省按 5 秒 × 1080P 档（4 倍系数）预扣。建议显式传 `seconds` 与 `size` 避免多扣（差额结算会修正，但预扣过多可能受账户余额限制）。
2. prime 带输入视频时**上游按输入时长收费**，网关已按同口径结算，调用方按实际账单理解即可。
3. 有视频输入时，上游约束"输入 + 输出总时长 ≤ 30 秒"，超出会被上游拒绝。

## 常见错误

| 现象 | 原因 / 处理 |
|------|------|
| failed：`InvalidParameter ... Input should be '1080P', '720P' or '480P': parameters.resolution` | `size` 传了非法格式（如 `1280x720` 小写 x）；改传 `480P`/`720P`/`1080P` 或 `1920*1080` 或 `16:9` |
| 400 `invalid_seconds` | `seconds` 不在 1~3600；智能时长 -1 请走 `metadata.parameters.duration` |
| failed：`The two modes are mutually exclusive...` | `first_frame`/`last_frame` 与 `reference_*` 素材混用；二选一 |
| failed：`Arrearage` / 余额类错误 | 上游资源问题或账户额度，稍后重试 |
| 任务长时间 `in_progress` | 正常出片耗时 1~5 分钟（1080P/长视频更久）；超 10 分钟仍无变化可反馈平台排查 |
| 产物链接 404 | OSS 链接 24 小时过期，完成后请及时转存 |

## 端到端示例

```bash
# 1. 创建任务（文生视频 720P 5 秒）
curl -X POST https://<gateway>/v1/videos \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "wan3.0-video",
    "prompt": "一只橘猫在窗台上晒太阳，慵懒地伸懒腰",
    "seconds": "5",
    "size": "720P"
  }'
# → {"id":"task_xxxx","status":"queued",...}

# 2. 轮询状态
curl https://<gateway>/v1/videos/task_xxxx -H "Authorization: Bearer sk-xxxx"
# → {"status":"in_progress","progress":30,...}
# → {"status":"completed","metadata":{"url":"https://dashscope-...mp4"}}

# 3. completed 后取产物（metadata.url，24 小时内下载）
```

智能时长示例：

```bash
curl -X POST https://<gateway>/v1/videos \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "wan3.0-video",
    "prompt": "一只小狗在草地上奔跑",
    "size": "480P",
    "metadata": {"parameters": {"duration": -1}}
  }'
```

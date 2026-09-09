# 灵枢AI 国内中转站 API 接入文档

面向中国大陆接入方（应用开发者 / 企业内部系统）。本文档覆盖国内中转站的**鉴权、模型调用、参数规范、计费与错误处理**。网关 100% 兼容 OpenAI 协议，大陆网络直连无需代理。

> 🚀 大陆直连：`https://token.nexuscore.net.cn` 部署于北京（阿里云），中国大陆全境直连，无出海链路。

## 目录

- [接入准备](#接入准备)
- [端点总览](#端点总览)
- [模型列表](#模型列表)
- [对话补全接口](#对话补全接口)
- [参数自动规范化](#参数自动规范化)
- [响应格式](#响应格式)
- [流式输出](#流式输出)
- [计费规则](#计费规则)
- [常见错误](#常见错误)
- [端到端示例](#端到端示例)
- [常见问题](#常见问题)

## 接入准备

| 项目 | 说明 |
|------|------|
| Base URL | `https://token.nexuscore.net.cn/v1` |
| 鉴权 | `Authorization: Bearer <平台令牌>`（sk- 开头，在[管理后台](https://token.nexuscore.net.cn/console/token)创建） |
| 请求格式 | 仅支持 JSON（`Content-Type: application/json`） |
| 网络要求 | 中国大陆直连（境外也可访问） |

### 获取令牌步骤

1. 登录管理后台 `https://token.nexuscore.net.cn`
2. 进入「令牌」页面，点击「添加令牌」
3. 设置名称与额度后创建，复制以 `sk-` 开头的完整令牌

## 端点总览

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/v1/models` | 列出当前令牌可用的模型 |
| POST | `/v1/chat/completions` | 对话补全（支持流式 / 非流式） |

## 模型列表

| 模型名（精确） | 类型 | 特点 |
|------|------|------|
| `kimi-k3` | 对话（深度推理） | 输出含思维链 `reasoning_content`，文本/多轮对话 |

> ⚠️ 模型名大小写敏感，必须精确匹配 `kimi-k3`（全小写）。

## 对话补全接口

```http
POST /v1/chat/completions
Content-Type: application/json
Authorization: Bearer <TOKEN>
```

### 请求参数

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | ✅ | 固定 `kimi-k3` |
| `messages` | array | ✅ | OpenAI 格式消息数组（system / user / assistant） |
| `max_tokens` | int | 建议 | 最大生成 token 数（含思维链部分） |
| `stream` | bool | 可选 | `true` 开启 SSE 流式 |
| `temperature` | number | 可选 | 会被网关自动规范化，见下节 |
| `top_p` | number | 可选 | 会被网关自动规范化，见下节 |
| `presence_penalty` | number | 可选 | 会被网关自动规范化，见下节 |
| `frequency_penalty` | number | 可选 | 会被网关自动规范化，见下节 |

> 其他 OpenAI 标准字段（如 `stop`、`tools` 等）按 OpenAI 协议透传。

### 最小请求示例

```json
{
  "model": "kimi-k3",
  "messages": [
    { "role": "user", "content": "你好，介绍一下你自己" }
  ],
  "max_tokens": 1024
}
```

## 参数自动规范化

上游模型对采样参数采用**白名单校验**（只接受单一合法值）。为免去调用方适配成本，网关在转发前会自动将以下参数覆盖为合法值——**无论传什么，调用都能成功**：

| 参数 | 传入任意值 | 网关实际下发 |
|------|------|------|
| `temperature` | 如 0.7 | `1` |
| `top_p` | 如 0.9 | `0.95` |
| `presence_penalty` | 如 0.5 | `0` |
| `frequency_penalty` | 如 0.5 | `0` |

调用方可以放心沿用 OpenAI SDK 的默认参数，无需修改。

## 响应格式

### 响应示例

```json
{
  "id": "e76fb503-417e-4f68-bf07-918f2a3aa862",
  "model": "kimi-k3",
  "object": "chat.completion",
  "created": 1788854641,
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "你好！我是 Kimi...",
        "reasoning_content": "用户发送了问候，我应该..."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 88,
    "completion_tokens": 14,
    "total_tokens": 102,
    "prompt_tokens_details": { "cached_tokens": 0 },
    "completion_tokens_details": { "reasoning_tokens": 12 }
  }
}
```

### 字段说明

| 字段 | 说明 |
|------|------|
| `choices[0].message.content` | 最终回答正文 |
| `choices[0].message.reasoning_content` | 思维链（深度推理过程），参与计费 |
| `usage.prompt_tokens` | 输入 token 数 |
| `usage.completion_tokens` | 输出 token 数（**含思维链 reasoning_tokens**） |
| `usage.prompt_tokens_details.cached_tokens` | 命中缓存的输入 token 数 |

## 流式输出

`stream: true` 时返回 SSE（`data:` 分帧），增量内容在 `delta.reasoning_content`（思维链）与 `delta.content`（正文）两个字段中，以 `[DONE]` 结束。

## 计费规则

按 token 用量计费，请求成功后自动从令牌额度中扣除：

| 计费项 | 单价 |
|------|------|
| 输入（含缓存命中部分） | **¥20 / 百万 tokens** |
| 输出（含思维链部分） | **¥100 / 百万 tokens** |

- 计费公式：`费用 = (输入 tokens × 20 + 输出 tokens × 100) ÷ 1,000,000`
- 实测锚点：输入 88 + 输出 14 tokens ≈ ¥0.0032
- 请求失败（上游 5xx / 网关错误）不扣费
- 后台「流水」页面可逐笔对账

## 常见错误

| HTTP | 现象 | 原因 / 处理 |
|------|------|------|
| 401 | `无效的令牌` | 令牌错误 / 已禁用；检查 Bearer 头与 sk- 前缀 |
| 404 | `model_not_found` | 模型名大小写错误；必须为 `kimi-k3` |
| 400 | `invalid_request` | 请求体字段非法（如缺 `messages`） |
| 429 | 余额不足 / 触发限流 | 充值或降低频率 |
| 502 | `upstream_exhausted` | 上游资源暂时不可用，稍后重试 |

## 端到端示例

```bash
# 1. 查看可用模型
curl https://token.nexuscore.net.cn/v1/models \
  -H "Authorization: Bearer sk-xxxx"

# 2. 对话（非流式）
curl -X POST https://token.nexuscore.net.cn/v1/chat/completions \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "kimi-k3",
    "messages": [{ "role": "user", "content": "你好" }],
    "max_tokens": 512
  }'
```

### Python（OpenAI SDK）

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://token.nexuscore.net.cn/v1",
    api_key="sk-xxxx",
)

resp = client.chat.completions.create(
    model="kimi-k3",
    messages=[{"role": "user", "content": "你好"}],
    max_tokens=512,
)
print(resp.choices[0].message.content)
```

### Node.js

```javascript
const resp = await fetch(
  "https://token.nexuscore.net.cn/v1/chat/completions",
  {
    method: "POST",
    headers: {
      Authorization: "Bearer sk-xxxx",
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model: "kimi-k3",
      messages: [{ role: "user", content: "你好" }],
      max_tokens: 512,
    }),
  }
);
const data = await resp.json();
console.log(data.choices[0].message.content);
```

## 常见问题

**Q：思维链 `reasoning_content` 需要展示给用户吗？**
不需要。它是模型推理过程，面向终端产品时建议只展示 `content`；但思维链参与输出计费。

**Q：max_tokens 设小了会怎样？**
思维链会优先消耗 max_tokens 预算，可能导致正文为空（`finish_reason: "length"`）。建议给足预算（如 ≥1024）。

**Q：支持多轮对话吗？**
支持。按 OpenAI 协议把历史消息放进 `messages` 数组即可。

**Q：境外服务器能调用吗？**
能。域名全球可达，只是定位为大陆直连低延迟。

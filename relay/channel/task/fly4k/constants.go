package fly4k

// 蝶变（fly4k）Seedance 视频任务适配器常量。
// 协议：https://api.fly4k.com 私有信封（code/msg/data），2026-09-28 官方文档版。

var ModelList = []string{
	// 蝶变4 Fast（超分）：上游写死模型，不透传；网关侧沿用官方 seedance 模型名计费/路由
	"doubao-seedance-2-0-260128",
}

var ChannelName = "fly4k"

// tokensPerSecond 基准档（720p）每秒产出 token 数（豆包 seedance 实测 720p/5s ≈ 108,900 token）。
// fly4k 不回显 usage，按「官方标准价 × 时长」计费：预扣 = 基准 250k token × 时长因子 × 档位倍率。
const tokensPerSecond = 21780.0

// durationMinSec / durationMaxSec 上游限制：视频时长仅支持 4~15 秒。
const (
	durationMinSec = 4
	durationMaxSec = 15
)

// supportedRatios 上游 ratio 合法值（必填，无默认档说明时按上游默认 16:9 兜底）。
var supportedRatios = map[string]bool{
	"16:9": true,
	"4:3":  true,
	"1:1":  true,
	"3:4":  true,
	"9:16": true,
	"21:9": true,
}

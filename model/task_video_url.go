package model

import (
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// ExtractUpstreamVideoURL 从任务存储的原始上游响应中递归提取上游真实视频地址。
//
// 任务 Data 字段的形态因渠道/上游而异，常见的有：
//   - 豆包原生格式:      {"id":"cgt-...","status":"succeeded","content":{"video_url":"https://..."}}
//   - new-api 通用信封:  {"code":"success","data":{ ...任务DTO, 其 data 内含 content.video_url / result_url... }}
//
// 全树优先寻找 "video_url"（最原始的内容字段），全树未命中再退 "result_url"；
// data: URI（base64 内联视频）一律跳过，交由调用方走代理回退逻辑。
// 找不到时返回空字符串。
func ExtractUpstreamVideoURL(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var node any
	if err := common.Unmarshal(data, &node); err != nil {
		return ""
	}
	for _, key := range []string{"video_url", "result_url"} {
		if url := findUpstreamVideoURL(node, key, 0); url != "" {
			return url
		}
	}
	return ""
}

const maxVideoURLSearchDepth = 8

func findUpstreamVideoURL(node any, key string, depth int) string {
	if depth > maxVideoURLSearchDepth {
		return ""
	}
	switch v := node.(type) {
	case map[string]any:
		if s, ok := v[key].(string); ok && strings.HasPrefix(s, "http") {
			return s
		}
		// 按 key 排序遍历，保证同一份 JSON 的提取结果确定
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if r := findUpstreamVideoURL(v[k], key, depth+1); r != "" {
				return r
			}
		}
	case []any:
		for _, item := range v {
			if r := findUpstreamVideoURL(item, key, depth+1); r != "" {
				return r
			}
		}
	}
	return ""
}

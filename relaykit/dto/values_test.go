package dto

import (
	"encoding/json"
	"testing"
)

type intValHolder struct {
	Duration IntValue `json:"duration"`
	Count    IntValue `json:"count"`
}

func TestIntValueUnmarshal(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantDur int
		wantCnt int
	}{
		{"整数", `{"duration":5,"count":3}`, 5, 3},
		{"浮点整数(5.0)", `{"duration":5.0,"count":2}`, 5, 2},
		{"浮点小数(4.5截断)", `{"duration":4.5,"count":1}`, 4, 1},
		{"字符串整数", `{"duration":"5","count":"3"}`, 5, 3},
		{"字符串浮点", `{"duration":"5.0","count":"1"}`, 5, 1},
		{"零值", `{"duration":0,"count":0}`, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h intValHolder
			if err := json.Unmarshal([]byte(c.json), &h); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if int(h.Duration) != c.wantDur {
				t.Errorf("duration = %d, want %d", h.Duration, c.wantDur)
			}
			if int(h.Count) != c.wantCnt {
				t.Errorf("count = %d, want %d", h.Count, c.wantCnt)
			}
		})
	}
}

// 复现线上 bug：maitoken 返回 usage.duration 为浮点数 5.0 时必须能解析
func TestIntValueMaitokenDuration(t *testing.T) {
	raw := `{"usage":{"video_count":1,"duration":5.0,"SR":720,"output_video_duration":5.0}}`
	var resp struct {
		Usage struct {
			VideoCount IntValue `json:"video_count"`
			Duration   IntValue `json:"duration"`
			SR         IntValue `json:"SR"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("maitoken usage unmarshal failed: %v", err)
	}
	if resp.Usage.Duration != 5 {
		t.Errorf("duration = %d, want 5", resp.Usage.Duration)
	}
	if resp.Usage.VideoCount != 1 {
		t.Errorf("video_count = %d, want 1", resp.Usage.VideoCount)
	}
	if resp.Usage.SR != 720 {
		t.Errorf("SR = %d, want 720", resp.Usage.SR)
	}
}

func TestIntValueMarshal(t *testing.T) {
	h := intValHolder{Duration: 5, Count: 3}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(b) != `{"duration":5,"count":3}` {
		t.Errorf("marshal result = %s", b)
	}
}

func TestIntValueInvalid(t *testing.T) {
	var h intValHolder
	if err := json.Unmarshal([]byte(`{"duration":"abc"}`), &h); err == nil {
		t.Error("expected error for invalid string value")
	}
}

func TestFloatValueUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		json string
		want float64
	}{
		{"整数", `{"v":5}`, 5},
		{"浮点", `{"v":4.5}`, 4.5},
		{"浮点整数", `{"v":5.0}`, 5},
		{"字符串数字", `{"v":"4.5"}`, 4.5},
		{"空字符串", `{"v":""}`, 0},
		{"零", `{"v":0}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h struct {
				V FloatValue `json:"v"`
			}
			if err := json.Unmarshal([]byte(c.json), &h); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if h.V.Float64() != c.want {
				t.Errorf("v = %v, want %v", h.V.Float64(), c.want)
			}
		})
	}
	// 非数字字符串必须报错
	var h struct {
		V FloatValue `json:"v"`
	}
	if err := json.Unmarshal([]byte(`{"v":"abc"}`), &h); err == nil {
		t.Error("expected error for invalid string value")
	}
}

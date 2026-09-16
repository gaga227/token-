package model

import "testing"

func TestExtractUpstreamVideoURLFromDoubaoNative(t *testing.T) {
	body := []byte(`{"id":"cgt-1","model":"doubao-seedance-2-0","status":"succeeded","content":{"video_url":"https://ark.example.com/a.mp4?sig=1"}}`)
	got := ExtractUpstreamVideoURL(body)
	if got != "https://ark.example.com/a.mp4?sig=1" {
		t.Fatalf("expected ark url, got %q", got)
	}
}

func TestExtractUpstreamVideoURLFromNewAPIEnvelope(t *testing.T) {
	// HK 实测形态：new-api 信封，任务 DTO 内层 data 才是上游原始响应
	body := []byte(`{"code":"success","data":{"task_id":"task_x","status":"SUCCESS","result_url":"https://ark.example.com/b.mp4?sig=2","data":{"content":{"video_url":"https://ark.example.com/c.mp4?sig=3"}}},"message":""}`)
	got := ExtractUpstreamVideoURL(body)
	// video_url 嵌套更深，但 key 优先级 video_url > result_url，应返回 c.mp4
	if got != "https://ark.example.com/c.mp4?sig=3" {
		t.Fatalf("expected inner video_url, got %q", got)
	}
}

func TestExtractUpstreamVideoURLSkipsDataURL(t *testing.T) {
	body := []byte(`{"content":{"video_url":"data:video/mp4;base64,AAAA"}}`)
	if got := ExtractUpstreamVideoURL(body); got != "" {
		t.Fatalf("data URI should be skipped, got %q", got)
	}
}

func TestExtractUpstreamVideoURLFallbackToResultURL(t *testing.T) {
	body := []byte(`{"code":"success","data":{"status":"SUCCESS","result_url":"https://ark.example.com/d.mp4?sig=4"}}`)
	got := ExtractUpstreamVideoURL(body)
	if got != "https://ark.example.com/d.mp4?sig=4" {
		t.Fatalf("expected result_url fallback, got %q", got)
	}
}

func TestExtractUpstreamVideoURLEmptyOrInvalid(t *testing.T) {
	if got := ExtractUpstreamVideoURL(nil); got != "" {
		t.Fatalf("nil should return empty, got %q", got)
	}
	if got := ExtractUpstreamVideoURL([]byte("not-json")); got != "" {
		t.Fatalf("invalid json should return empty, got %q", got)
	}
	if got := ExtractUpstreamVideoURL([]byte(`{"status":"succeeded"}`)); got != "" {
		t.Fatalf("no url should return empty, got %q", got)
	}
}

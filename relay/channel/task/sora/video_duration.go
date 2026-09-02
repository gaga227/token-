package sora

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abema/go-mp4"
)

// ============================
// 本地文件时长解析
// ============================

// GetInputVideoDuration 解析输入视频文件的实际时长（秒）。
// 仅支持 ISO BMFF 容器（mp4 / mov / m4v），通过读取 mvhd box 的
// Duration / Timescale 计算；其余格式（webm / mkv 等）返回错误，
// 由调用方决定 fallback 策略（近似为输出秒数）。
//
// 实现复用项目已有依赖 github.com/abema/go-mp4（common/audio.go 中
// 解析音频 m4a/mp4 时长使用的同一库），纯 Go 无外部二进制依赖。
func GetInputVideoDuration(r io.ReadSeeker, filename string) (float64, error) {
	ext := strings.ToLower(filename)
	if !strings.HasSuffix(ext, ".mp4") && !strings.HasSuffix(ext, ".mov") && !strings.HasSuffix(ext, ".m4v") {
		return 0, errors.New("unsupported input video container: " + filename)
	}

	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	info, err := mp4.Probe(r)
	if err != nil {
		return 0, err
	}
	if info.Timescale <= 0 || info.Duration <= 0 {
		return 0, errors.New("invalid mvhd timescale/duration")
	}
	return float64(info.Duration) / float64(info.Timescale), nil
}

// ============================
// 远程 URL 视频时长解析（HTTP Range 仅读元数据，不下载全片）
// ============================

const (
	remoteVideoProbeTimeout = 15 * time.Second
	remoteVideoMaxSize      = 1 << 30 // 1GB，超出视为异常输入
)

// GetRemoteVideoDuration 解析远程视频 URL 的实际时长（秒）。
// 通过 HTTP Range 请求把远程文件映射为 ReadSeeker 交给 mp4.Probe，
// 仅拉取 box 元数据（ftyp + moov/mvhd），不下载整个视频文件。
// 服务器不支持 Range 或非 MP4/MOV 时返回错误。
func GetRemoteVideoDuration(ctx context.Context, url string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteVideoProbeTimeout)
	defer cancel()

	client := &http.Client{Timeout: remoteVideoProbeTimeout}

	// 探测：Range 请求首字节，验证服务器支持 Range 并获取文件总大小
	headReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	headReq.Header.Set("Range", "bytes=0-0")
	headReq.Header.Set("User-Agent", "new-api-video-probe")
	resp, err := client.Do(headReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("server does not support range request (status %d)", resp.StatusCode)
	}

	// 从 Content-Range 拿总大小（bytes 0-0/总大小）
	contentRange := resp.Header.Get("Content-Range")
	totalSize := int64(0)
	if idx := strings.LastIndex(contentRange, "/"); idx >= 0 {
		totalSize, _ = strconv.ParseInt(strings.TrimSpace(contentRange[idx+1:]), 10, 64)
	}
	if totalSize <= 0 {
		return 0, errors.New("cannot determine remote file size")
	}
	if totalSize > remoteVideoMaxSize {
		return 0, fmt.Errorf("remote video too large: %d bytes", totalSize)
	}

	rs := &remoteReadSeeker{
		url:    url,
		size:   totalSize,
		client: client,
		ctx:    ctx,
	}
	return GetInputVideoDuration(rs, url)
}

// remoteReadSeeker 基于 HTTP Range 实现 io.ReadSeeker：Seek 只更新游标不产生请求，
// Read 时按需发送 Range 请求拉取对应字节块，并做简单块缓存避免重复拉取。
type remoteReadSeeker struct {
	url    string
	size   int64
	pos    int64
	client *http.Client
	ctx    context.Context

	cache    map[int64][]byte // 块缓存：起始偏移 → 数据
	cacheLen int
}

const remoteReadChunk = 64 * 1024

func (r *remoteReadSeeker) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && r.pos < r.size {
		chunkStart := r.pos / remoteReadChunk * remoteReadChunk
		data, ok := r.cache[chunkStart]
		if !ok {
			if len(r.cache) >= 8 { // 缓存最多 8 块，防止重复拉取
				r.cache = map[int64][]byte{}
			}
			if r.cache == nil {
				r.cache = map[int64][]byte{}
			}
			end := chunkStart + remoteReadChunk - 1
			if end >= r.size {
				end = r.size - 1
			}
			req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
			if err != nil {
				return n, err
			}
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", chunkStart, end))
			req.Header.Set("User-Agent", "new-api-video-probe")
			resp, err := r.client.Do(req)
			if err != nil {
				return n, err
			}
			if resp.StatusCode != http.StatusPartialContent {
				resp.Body.Close()
				return n, fmt.Errorf("range request failed: status %d", resp.StatusCode)
			}
			data, err = io.ReadAll(io.LimitReader(resp.Body, remoteReadChunk))
			resp.Body.Close()
			if err != nil {
				return n, err
			}
			r.cache[chunkStart] = data
		}
		off := r.pos - chunkStart
		if off < int64(len(data)) {
			c := copy(p[n:], data[off:])
			n += c
			r.pos += int64(c)
		} else {
			return n, io.EOF
		}
	}
	return n, nil
}

func (r *remoteReadSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.pos = offset
	case io.SeekCurrent:
		r.pos += offset
	case io.SeekEnd:
		r.pos = r.size + offset
	}
	if r.pos < 0 {
		r.pos = 0
	}
	return r.pos, nil
}

// IsVideoReference 判断 input_reference / 图片 URL 是否为视频。
// 按 URL 扩展名判断：.mp4/.mov/.m4v/.webm/.mkv 视为视频，其余（含图片、无扩展名）视为图片。
func IsVideoReference(ref string) bool {
	lower := strings.ToLower(ref)
	if i := strings.Index(lower, "?"); i >= 0 {
		lower = lower[:i]
	}
	for _, ext := range []string{".mp4", ".mov", ".m4v", ".webm", ".mkv", ".avi", ".flv", ".ts"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

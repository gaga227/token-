package common

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Upstream asset library channels (volcengine ark / kuaizi Assets API) reject
// videos whose frame rate falls outside this window.
const (
	AssetVideoMinFPS = 23.8
	AssetVideoMaxFPS = 60.0
)

// Upstream asset library channels reject videos whose duration falls outside
// this window.
const (
	AssetVideoMinDurationSec = 1.8
	AssetVideoMaxDurationSec = 30.2
)

// ErrAssetVideoFPSInvalid marks a rejected video upload whose frame rate is
// outside the upstream window.
var ErrAssetVideoFPSInvalid = errors.New("asset video frame rate is out of range")

// ErrAssetVideoDurationInvalid marks a rejected video upload whose duration is
// outside the upstream window.
var ErrAssetVideoDurationInvalid = errors.New("asset video duration is out of range")

// ValidateAssetVideo enforces the upstream frame-rate and duration windows on
// the raw bytes of a video file. Containers whose metrics cannot be determined
// locally (e.g. WebM) pass so the upstream keeps the final say.
func ValidateAssetVideo(data []byte) error {
	fps, duration, ok := DetectAssetVideoInfo(data)
	if !ok {
		return nil
	}
	if fps < AssetVideoMinFPS || fps > AssetVideoMaxFPS {
		return fmt.Errorf("%w: 视频帧率为 %.1f FPS，需在 23.8–60 FPS 之间，请重新转码后上传", ErrAssetVideoFPSInvalid, fps)
	}
	if duration < AssetVideoMinDurationSec || duration > AssetVideoMaxDurationSec {
		return fmt.Errorf("%w: 视频时长为 %.1f 秒，需在 1.8–30.2 秒之间，请裁剪后上传", ErrAssetVideoDurationInvalid, duration)
	}
	return nil
}

// validateStoredAssetVideo probes a locally stored video file. Read
// failures fail open for the same reason as unparseable containers.
func validateStoredAssetVideo(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<30))
	if err != nil {
		return nil
	}
	return ValidateAssetVideo(data)
}

// DetectAssetVideoInfo parses an ISO-BMFF container (MP4/MOV) and returns the
// average frame rate and duration (seconds) of the first video track. The
// frame rate is computed as sample count / media duration. ok is false when
// the container is not MP4-like or the metrics cannot be determined.
func DetectAssetVideoInfo(data []byte) (fps float64, duration float64, ok bool) {
	var moov []byte
	iterateMP4Boxes(data, func(boxType string, payload []byte) bool {
		if boxType == "moov" {
			moov = payload
			return false
		}
		return true
	})
	if moov == nil {
		return 0, 0, false
	}
	var found bool
	var resultFPS, resultDuration float64
	iterateMP4Boxes(moov, func(boxType string, payload []byte) bool {
		if boxType != "trak" {
			return true
		}
		trackFPS, trackDuration, trackOK := mp4TrackVideoInfo(payload)
		if trackOK {
			resultFPS, resultDuration, found = trackFPS, trackDuration, true
			return false
		}
		return true
	})
	return resultFPS, resultDuration, found
}

// mp4TrackVideoInfo extracts the average frame rate and duration (seconds)
// from a single trak box.
func mp4TrackVideoInfo(trak []byte) (float64, float64, bool) {
	var (
		isVideo     bool
		timescale   uint32
		duration    uint64
		hasDuration bool
		sampleCount uint32
		hasCount    bool
	)
	iterateMP4Boxes(trak, func(boxType string, payload []byte) bool {
		if boxType != "mdia" {
			return true
		}
		iterateMP4Boxes(payload, func(subType string, subPayload []byte) bool {
			switch subType {
			case "hdlr":
				if len(subPayload) >= 12 && string(subPayload[8:12]) == "vide" {
					isVideo = true
				}
			case "mdhd":
				if len(subPayload) >= 20 && subPayload[0] == 1 {
					if len(subPayload) >= 32 {
						timescale = binary.BigEndian.Uint32(subPayload[20:24])
						duration = binary.BigEndian.Uint64(subPayload[24:32])
						hasDuration = true
					}
				} else if len(subPayload) >= 20 {
					timescale = binary.BigEndian.Uint32(subPayload[12:16])
					duration = uint64(binary.BigEndian.Uint32(subPayload[16:20]))
					hasDuration = true
				}
			case "minf":
				iterateMP4Boxes(subPayload, func(minfType string, minfPayload []byte) bool {
					if minfType != "stbl" {
						return true
					}
					iterateMP4Boxes(minfPayload, func(stblType string, stblPayload []byte) bool {
						if stblType == "stsz" && len(stblPayload) >= 12 {
							sampleCount = binary.BigEndian.Uint32(stblPayload[8:12])
							hasCount = true
						}
						return true
					})
					return true
				})
			}
			return true
		})
		return true
	})
	if !isVideo || !hasCount || !hasDuration || timescale == 0 || duration == 0 || sampleCount == 0 {
		return 0, 0, false
	}
	trackDuration := float64(duration) / float64(timescale)
	return float64(sampleCount) * float64(timescale) / float64(duration), trackDuration, true
}

// iterateMP4Boxes walks the immediate children of an ISO-BMFF container and
// invokes fn for each box with its payload. Truncated or malformed boxes stop
// the walk. Returning false from fn stops iteration early.
func iterateMP4Boxes(data []byte, fn func(boxType string, payload []byte) bool) {
	pos := 0
	for pos+8 <= len(data) {
		size := binary.BigEndian.Uint32(data[pos : pos+4])
		boxType := string(data[pos+4 : pos+8])
		header := 8
		switch size {
		case 0:
			size = uint32(len(data) - pos)
		case 1:
			if pos+16 > len(data) {
				return
			}
			large := binary.BigEndian.Uint64(data[pos+8 : pos+16])
			if large > uint64(len(data)-pos) {
				large = uint64(len(data) - pos)
			}
			size = uint32(large)
			header = 16
		}
		if int64(size) < int64(header) || pos+int(size) > len(data) {
			return
		}
		if !fn(boxType, data[pos+header:pos+int(size)]) {
			return
		}
		pos += int(size)
	}
}

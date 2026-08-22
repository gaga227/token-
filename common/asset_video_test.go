package common

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildMP4Box assembles a minimal ISO-BMFF box for tests.
func buildMP4Box(boxType string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], boxType)
	copy(box[8:], payload)
	return box
}

// buildMP4Video builds a minimal MP4 whose single video track has
// sampleCount frames over duration/timescale seconds. The audio track is
// included to make sure only video tracks are considered.
func buildMP4Video(sampleCount uint32, timescale uint32, duration uint32, moovAtEnd bool) []byte {
	buildTrack := func(handler string, count uint32) []byte {
		hdlr := make([]byte, 24)
		copy(hdlr[8:12], handler)
		mdhd := make([]byte, 20)
		binary.BigEndian.PutUint32(mdhd[12:16], timescale)
		binary.BigEndian.PutUint32(mdhd[16:20], duration)
		stsz := make([]byte, 12)
		binary.BigEndian.PutUint32(stsz[8:12], count)
		mdia := bytes.Join([][]byte{
			buildMP4Box("hdlr", hdlr),
			buildMP4Box("mdhd", mdhd),
			buildMP4Box("minf", buildMP4Box("stbl", buildMP4Box("stsz", stsz))),
		}, nil)
		return buildMP4Box("trak", buildMP4Box("mdia", mdia))
	}
	video := buildTrack("vide", sampleCount)
	audio := buildTrack("soun", 1024)
	moov := buildMP4Box("moov", bytes.Join([][]byte{video, audio}, nil))
	ftyp := buildMP4Box("ftyp", []byte("mp42\x00\x00\x02\x00mp42isom"))
	mdat := buildMP4Box("mdat", bytes.Repeat([]byte{0}, 64))
	if moovAtEnd {
		return bytes.Join([][]byte{ftyp, mdat, moov}, nil)
	}
	return bytes.Join([][]byte{ftyp, moov, mdat}, nil)
}

func TestDetectAssetVideoInfo(t *testing.T) {
	// 200 samples over 10s => 20 fps.
	fps, duration, ok := DetectAssetVideoInfo(buildMP4Video(200, 1000, 10000, false))
	require.True(t, ok)
	assert.InDelta(t, 20.0, fps, 0.001)
	assert.InDelta(t, 10.0, duration, 0.001)

	// moov at the end of the file is still found.
	fps, duration, ok = DetectAssetVideoInfo(buildMP4Video(300, 600, 6000, true))
	require.True(t, ok)
	assert.InDelta(t, 30.0, fps, 0.001)
	assert.InDelta(t, 10.0, duration, 0.001)

	// Non-MP4 payloads are not detectable.
	_, _, ok = DetectAssetVideoInfo([]byte("\x1a\x45\xdf\xa3webm-data"))
	assert.False(t, ok)
	_, _, ok = DetectAssetVideoInfo(nil)
	assert.False(t, ok)
}

func TestValidateAssetVideo(t *testing.T) {
	// 20 fps is below the 23.8 floor.
	err := ValidateAssetVideo(buildMP4Video(200, 1000, 10000, false))
	require.ErrorIs(t, err, ErrAssetVideoFPSInvalid)
	assert.Contains(t, err.Error(), "20.0 FPS")
	assert.Contains(t, err.Error(), "23.8")

	// 61 fps is above the 60 ceiling.
	err = ValidateAssetVideo(buildMP4Video(610, 1000, 10000, false))
	assert.ErrorIs(t, err, ErrAssetVideoFPSInvalid)

	// 30 fps over 10s passes.
	assert.NoError(t, ValidateAssetVideo(buildMP4Video(300, 1000, 10000, false)))

	// Unparseable containers fail open.
	assert.NoError(t, ValidateAssetVideo([]byte("not a video at all")))
}

func TestValidateAssetVideoDuration(t *testing.T) {
	// 30 fps over 45s => duration above the 30.2s ceiling.
	err := ValidateAssetVideo(buildMP4Video(1350, 1000, 45000, false))
	require.ErrorIs(t, err, ErrAssetVideoDurationInvalid)
	assert.Contains(t, err.Error(), "45.0 秒")
	assert.Contains(t, err.Error(), "30.2")

	// 30 fps over 1s => duration below the 1.8s floor.
	err = ValidateAssetVideo(buildMP4Video(30, 1000, 1000, false))
	assert.ErrorIs(t, err, ErrAssetVideoDurationInvalid)

	// 30 fps over 5s passes.
	assert.NoError(t, ValidateAssetVideo(buildMP4Video(150, 1000, 5000, false)))
}

func TestSaveAssetStorageFileRejectsLowFPSVideo(t *testing.T) {
	withTempAssetStorageDir(t)
	_, _, _, err := SaveAssetStorageFile(bytes.NewReader(buildMP4Video(200, 1000, 10000, false)))
	require.ErrorIs(t, err, ErrAssetVideoFPSInvalid)

	// The rejected file must have been cleaned up.
	err = filepath.Walk(AssetStorageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			t.Errorf("leftover file after rejected video upload: %s", path)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestSaveAssetStorageFileRejectsLongDurationVideo(t *testing.T) {
	withTempAssetStorageDir(t)
	_, _, _, err := SaveAssetStorageFile(bytes.NewReader(buildMP4Video(1350, 1000, 45000, false)))
	require.ErrorIs(t, err, ErrAssetVideoDurationInvalid)

	// The rejected file must have been cleaned up.
	err = filepath.Walk(AssetStorageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			t.Errorf("leftover file after rejected video upload: %s", path)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestSaveAssetStorageFileAcceptsHighFPSVideo(t *testing.T) {
	withTempAssetStorageDir(t)
	key, assetType, size, err := SaveAssetStorageFile(bytes.NewReader(buildMP4Video(150, 1000, 5000, false)))
	require.NoError(t, err)
	assert.Equal(t, "Video", assetType)
	assert.Regexp(t, `^[0-9a-f]{2}/[0-9a-f]{32}\.mp4$`, key)
	assert.Positive(t, size)
	require.NoError(t, DeleteAssetStorageFile(key))
}

func TestValidateAssetVideoFPSRealWorldSampleShape(t *testing.T) {
	// The failing upload from 2026-08-22 was 10.167s with 204 frames
	// (timescale 600, duration 6100) => ~20.07 fps.
	data := buildMP4Video(204, 600, 6100, false)
	fps, _, ok := DetectAssetVideoInfo(data)
	require.True(t, ok)
	assert.InDelta(t, 20.066, fps, 0.01)
	err := ValidateAssetVideo(data)
	require.ErrorIs(t, err, ErrAssetVideoFPSInvalid)
	assert.Contains(t, err.Error(), fmt.Sprintf("%.1f", fps))
}

func TestValidateAssetVideoDurationRealWorldSampleShape(t *testing.T) {
	// The failing upload from 2026-08-22 was 44.77s (timescale 90000,
	// duration 4029500) at 30 fps.
	data := buildMP4Video(1343, 90000, 4029500, false)
	_, duration, ok := DetectAssetVideoInfo(data)
	require.True(t, ok)
	assert.InDelta(t, 44.77, duration, 0.01)
	err := ValidateAssetVideo(data)
	require.ErrorIs(t, err, ErrAssetVideoDurationInvalid)
	assert.Contains(t, err.Error(), "44.8 秒")
}

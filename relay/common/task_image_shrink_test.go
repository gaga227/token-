package common

import (
	"bytes"
	"image"
	"image/jpeg"
	"math/rand"
	"testing"

	platformcommon "github.com/QuantumNous/new-api/common"
)

// 任务提交前，请求里所有指向本网关资产库的首帧/尾帧/参考图都要经过瘦身，
// 避免上游回源拉取数 MB 原图超时。这里验证字段遍历没有遗漏。

func TestShrinkTaskRequestImagesCoversAllImageFields(t *testing.T) {
	origDir := platformcommon.AssetStorageDir
	platformcommon.AssetStorageDir = t.TempDir()
	t.Cleanup(func() { platformcommon.AssetStorageDir = origDir })

	big := func() string {
		img := image.NewRGBA(image.Rect(0, 0, 2600, 1700))
		raw := make([]byte, 2600*1700*4)
		if _, err := rand.New(rand.NewSource(7)).Read(raw); err != nil {
			t.Fatalf("rand: %v", err)
		}
		for i := 3; i < len(raw); i += 4 {
			raw[i] = 255
		}
		copy(img.Pix, raw)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
			t.Fatalf("encode: %v", err)
		}
		key, _, _, err := platformcommon.SaveAssetStorageFile(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		return "https://gw.example.com" + platformcommon.AssetFileURLPrefix + key
	}

	bigURL := big()
	req := &TaskSubmitReq{
		Image:              bigURL,
		Images:             []string{bigURL},
		LastFrameImageURL:  bigURL,
		ReferenceImageURLs: []string{bigURL},
		InputReference:     bigURL,
	}
	shrinkTaskRequestImages(req)

	checks := map[string]string{
		"Image":              req.Image,
		"Images[0]":          req.Images[0],
		"LastFrameImageURL":  req.LastFrameImageURL,
		"ReferenceImageURLs": req.ReferenceImageURLs[0],
		"InputReference":     req.InputReference,
	}
	for name, got := range checks {
		if got == bigURL {
			t.Fatalf("%s was not shrunk", name)
		}
	}

	// nil 请求必须被容忍（防御性），不能 panic。
	shrinkTaskRequestImages(nil)
}

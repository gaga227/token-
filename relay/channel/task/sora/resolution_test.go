package sora

import "testing"

// TestResolutionFromSizeFlatH3 verifies the flat-H3 resolution enum mapping
// for the OpenAI-style "WxH" size input. maitoken accepts ONLY 480p and
// 720p (returns 422 "resolution must be 480p or 720p" otherwise). The
// rendered quality is 768P-class, but the API field name is 720p — do not
// change to "768p".
func TestResolutionFromSizeFlatH3(t *testing.T) {
	cases := []struct {
		name string
		size string
		want string
	}{
		{"portrait 720P", "720x1280", "720p"},
		{"landscape 720P", "1280x720", "720p"},
		{"square 1024 falls to 480P (long edge < 1280)", "1024x1024", "480p"},
		{"portrait 2K still 720P for flat H3", "1440x2560", "720p"},
		{"landscape 2K still 720P for flat H3", "2560x1440", "720p"},
		{"empty falls back to 720P", "", "720p"},
		{"garbage falls back to 720P", "not-a-size", "720p"},
		// 480P-class sizes map to 480p per the API contract.
		{"small portrait 480P", "540x960", "480p"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolutionFromSize(tc.size); got != tc.want {
				t.Errorf("resolutionFromSize(%q) = %q, want %q", tc.size, got, tc.want)
			}
		})
	}
}

// TestBaseFastResolutionNotForced verifies the 2026-09-10 contract change:
// maitoken's new doc states all three self-deployed tiers accept only 720p
// (768P input is converted to 720p platform-side), so the gateway no longer
// forces resolution=768P for minimax-h3-base-fast — the client value (or the
// size-derived mapping) passes through untouched.
func TestBaseFastResolutionNotForced(t *testing.T) {
	cases := []struct {
		name string
		body map[string]interface{}
		want interface{}
	}{
		{"size mapping kept", map[string]interface{}{
			"model": "minimax-h3-base-fast", "prompt": "p", "seconds": "5", "size": "720x1280",
		}, "720p"},
		{"direct 720p kept", map[string]interface{}{
			"model": "minimax-h3-base-fast", "prompt": "p", "seconds": "5", "resolution": "720p",
		}, "720p"},
		{"direct 768P kept for platform conversion", map[string]interface{}{
			"model": "minimax-h3-base-fast", "prompt": "p", "seconds": "5", "resolution": "768P",
		}, "768P"},
		{"no resolution stays empty", map[string]interface{}{
			"model": "minimax-h3-base-fast", "prompt": "p", "seconds": "5",
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := sanitizeVideoRequestBody(tc.body, "minimax-h3-base-fast"); err != nil {
				t.Fatalf("sanitizeVideoRequestBody returned error: %v", err)
			}
			if got := tc.body["resolution"]; got != tc.want {
				t.Errorf("resolution = %v, want %v", got, tc.want)
			}
		})
	}

	// Sibling models keep the legacy enum and are likewise not forced.
	for _, m := range []string{"minimax-h3-base", "minimax-h3-mini"} {
		body := map[string]interface{}{
			"model": m, "prompt": "p", "seconds": "5", "size": "720x1280",
		}
		if err := sanitizeVideoRequestBody(body, m); err != nil {
			t.Fatalf("sanitizeVideoRequestBody(%s) returned error: %v", m, err)
		}
		if got := body["resolution"]; got != "720p" {
			t.Errorf("%s resolution = %v, want 720p", m, got)
		}
	}
}

// TestGenerationModeTranslated locks in the unified-field handling: the
// gateway translates maitoken's generation_mode (t2v/i2v/r2v) into the flat
// mode enum and removes the original field, so upstream never receives both
// fields (their conflict priority is undefined per the doc). An explicit
// client-provided mode always wins.
func TestGenerationModeTranslated(t *testing.T) {
	cases := []struct {
		name string
		body map[string]interface{}
		wantMode string
	}{
		{"t2v becomes t2va", map[string]interface{}{
			"model": "minimax-h3-base", "prompt": "p", "seconds": "5", "generation_mode": "t2v",
		}, "t2va"},
		{"i2v becomes i2va", map[string]interface{}{
			"model": "minimax-h3-base", "prompt": "p", "seconds": "5", "generation_mode": "i2v",
		}, "i2va"},
		{"r2v becomes ref2va", map[string]interface{}{
			"model": "minimax-h3-base", "prompt": "p", "seconds": "5", "generation_mode": "r2v",
		}, "ref2va"},
		{"explicit mode wins over generation_mode", map[string]interface{}{
			"model": "minimax-h3-base", "prompt": "p", "seconds": "5",
			"generation_mode": "t2v", "mode": "i2va",
		}, "i2va"},
		{"unknown generation_mode dropped, mode derived", map[string]interface{}{
			"model": "minimax-h3-base", "prompt": "p", "seconds": "5", "generation_mode": "x9z",
		}, "t2va"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.body["model"].(string)
			if err := sanitizeVideoRequestBody(tc.body, m); err != nil {
				t.Fatalf("sanitizeVideoRequestBody returned error: %v", err)
			}
			if _, exists := tc.body["generation_mode"]; exists {
				t.Errorf("generation_mode should be removed, still present: %v", tc.body["generation_mode"])
			}
			if got := tc.body["mode"]; got != tc.wantMode {
				t.Errorf("mode = %v, want %v", got, tc.wantMode)
			}
		})
	}
}

// TestLastFrameAppendedToImages verifies first+last-frame generation keeps
// both frames: image goes first, last_frame_image_url is appended to the end
// of the images list (previously the field was silently dropped), and the
// derived mode is fl2va.
func TestLastFrameAppendedToImages(t *testing.T) {
	body := map[string]interface{}{
		"model": "minimax-h3-base", "prompt": "p", "seconds": "5",
		"image": "https://example.com/first.png",
		"last_frame_image_url": "https://example.com/last.png",
	}
	if err := sanitizeVideoRequestBody(body, "minimax-h3-base"); err != nil {
		t.Fatalf("sanitizeVideoRequestBody returned error: %v", err)
	}
	if _, exists := body["last_frame_image_url"]; exists {
		t.Error("last_frame_image_url should be removed")
	}
	images, ok := body["images"].([]interface{})
	if !ok || len(images) != 2 {
		t.Fatalf("images = %v, want 2 entries", body["images"])
	}
	if images[0] != "https://example.com/first.png" || images[1] != "https://example.com/last.png" {
		t.Errorf("images order = %v, want [first, last]", images)
	}
	if got := body["mode"]; got != "fl2va" {
		t.Errorf("mode = %v, want fl2va", got)
	}
}

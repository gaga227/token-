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

// TestBaseFastForced768P locks in maitoken's confirmed contract: the flat
// minimax-h3-base-fast model must always receive resolution "768P" upstream,
// no matter whether the client sent a size, a direct resolution, or nothing.
func TestBaseFastForced768P(t *testing.T) {
	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"size mapping overridden", map[string]interface{}{
			"model": "minimax-h3-base-fast", "prompt": "p", "seconds": "5", "size": "720x1280",
		}},
		{"direct 720p overridden", map[string]interface{}{
			"model": "minimax-h3-base-fast", "prompt": "p", "seconds": "5", "resolution": "720p",
		}},
		{"no resolution at all", map[string]interface{}{
			"model": "minimax-h3-base-fast", "prompt": "p", "seconds": "5",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := sanitizeVideoRequestBody(tc.body, "minimax-h3-base-fast"); err != nil {
				t.Fatalf("sanitizeVideoRequestBody returned error: %v", err)
			}
			if got := tc.body["resolution"]; got != "768P" {
				t.Errorf("resolution = %v, want 768P", got)
			}
		})
	}

	// Sibling models must keep the legacy enum and NOT be forced to 768P.
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

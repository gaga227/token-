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

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func postEncode(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/encode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	NewRouter().ServeHTTP(w, req)
	return w
}

func postLayout(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/layout", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	NewRouter().ServeHTTP(w, req)
	return w
}

func TestHealthz(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	NewRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != `{"status":"ok"}` {
		t.Errorf("got %d %s", w.Code, w.Body.String())
	}
}

// TestEncodeGoldenBody pins the exact response body for a mixed input:
// uppercase, number run, lowercase, space, newline, and 0-as-J.
func TestEncodeGoldenBody(t *testing.T) {
	w := postEncode(t, `{"text":"A1b 23\nZz0"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"items":[` +
		`{"source_index":0,"source":"A","kind":"character","prefix_type":"none","cells":[1]},` +
		`{"source_index":1,"source":"1","kind":"character","prefix_type":"number","cells":[60,1]},` +
		`{"source_index":2,"source":"b","kind":"character","prefix_type":"capital","cells":[32,3]},` +
		`{"source_index":3,"source":" ","kind":"character","prefix_type":"none","cells":[0]},` +
		`{"source_index":4,"source":"2","kind":"character","prefix_type":"number","cells":[60,3]},` +
		`{"source_index":5,"source":"3","kind":"character","prefix_type":"none","cells":[9]},` +
		`{"source_index":6,"source":"\n","kind":"newline","prefix_type":"none","cells":[]},` +
		`{"source_index":7,"source":"Z","kind":"character","prefix_type":"none","cells":[53]},` +
		`{"source_index":8,"source":"z","kind":"character","prefix_type":"capital","cells":[32,53]},` +
		`{"source_index":9,"source":"0","kind":"character","prefix_type":"number","cells":[60,26]}` +
		`]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

func TestEncodeEmptyText422(t *testing.T) {
	w := postEncode(t, `{"text":""}`)
	assertError(t, w, http.StatusUnprocessableEntity, "empty_text", nil)
}

func TestEncodeMissingText422(t *testing.T) {
	w := postEncode(t, `{}`)
	assertError(t, w, http.StatusUnprocessableEntity, "empty_text", nil)
}

func TestEncodeTooLongBoundary(t *testing.T) {
	w := postEncode(t, `{"text":"`+strings.Repeat("a", 2000)+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("2000 code points: got %d", w.Code)
	}
	var resp struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Items) != 2000 {
		t.Fatalf("2000 code points: got %d items, err=%v", len(resp.Items), err)
	}

	w = postEncode(t, `{"text":"`+strings.Repeat("a", 2001)+`"}`)
	assertError(t, w, http.StatusUnprocessableEntity, "text_too_long", nil)
}

// TestEncodeInvalidChar422 checks whole-request rejection, the reported
// first illegal index, and the absence of any partial encoding.
func TestEncodeInvalidChar422(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantIndex int
	}{
		{"hanzi", `{"text":"ab汉cd"}`, 2},
		{"tab", `{"text":"a\tb"}`, 1},
		{"carriage return", `{"text":"a\rb"}`, 1},
		{"punctuation", `{"text":"Hi!"}`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postEncode(t, tc.body)
			assertError(t, w, http.StatusUnprocessableEntity, "invalid_character", &tc.wantIndex)
		})
	}
}

func TestEncodeMalformedJSON400(t *testing.T) {
	for _, body := range []string{`{not json`, `{"text": 42}`, `["a"]`} {
		w := postEncode(t, body)
		assertError(t, w, http.StatusBadRequest, "bad_request", nil)
	}
}

// TestLayoutGoldenBody pins the exact preview body for a mixed input at
// width 2: the double-cell "b" wraps unsplit, the explicit newline ends
// line 1, and the number run "12" splits across lines 2-3 without
// re-indication.
func TestLayoutGoldenBody(t *testing.T) {
	w := postLayout(t, `{"text":"Ab\n12","cells_per_line":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"lines":[` +
		`{"line_index":0,"items":[` +
		`{"source_index":0,"source":"A","kind":"character","prefix_type":"none","cells":[1]}` +
		`]},` +
		`{"line_index":1,"items":[` +
		`{"source_index":1,"source":"b","kind":"character","prefix_type":"capital","cells":[32,3]},` +
		`{"source_index":2,"source":"\n","kind":"newline","prefix_type":"none","cells":[]}` +
		`]},` +
		`{"line_index":2,"items":[` +
		`{"source_index":3,"source":"1","kind":"character","prefix_type":"number","cells":[60,1]}` +
		`]},` +
		`{"line_index":3,"items":[` +
		`{"source_index":4,"source":"2","kind":"character","prefix_type":"none","cells":[3]}` +
		`]}` +
		`]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

// TestLayoutConsecutiveNewlinesBody pins the visible empty line left by a
// run of newlines.
func TestLayoutConsecutiveNewlinesBody(t *testing.T) {
	w := postLayout(t, `{"text":"a\n\nb","cells_per_line":10}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"lines":[` +
		`{"line_index":0,"items":[` +
		`{"source_index":0,"source":"a","kind":"character","prefix_type":"capital","cells":[32,1]},` +
		`{"source_index":1,"source":"\n","kind":"newline","prefix_type":"none","cells":[]}` +
		`]},` +
		`{"line_index":1,"items":[` +
		`{"source_index":2,"source":"\n","kind":"newline","prefix_type":"none","cells":[]}` +
		`]},` +
		`{"line_index":2,"items":[` +
		`{"source_index":3,"source":"b","kind":"character","prefix_type":"capital","cells":[32,3]}` +
		`]}` +
		`]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

// TestLayoutTrailingNewlineBody pins the empty final line a trailing
// newline leaves behind, serialized with "items":[].
func TestLayoutTrailingNewlineBody(t *testing.T) {
	w := postLayout(t, `{"text":"a\n","cells_per_line":10}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"lines":[` +
		`{"line_index":0,"items":[` +
		`{"source_index":0,"source":"a","kind":"character","prefix_type":"capital","cells":[32,1]},` +
		`{"source_index":1,"source":"\n","kind":"newline","prefix_type":"none","cells":[]}` +
		`]},` +
		`{"line_index":1,"items":[]}` +
		`]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

// TestLayoutLineWidthBoundaries accepts the extremes of the allowed range.
func TestLayoutLineWidthBoundaries(t *testing.T) {
	for _, width := range []int{2, 80} {
		body := `{"text":"ab","cells_per_line":` + strconv.Itoa(width) + `}`
		w := postLayout(t, body)
		if w.Code != http.StatusOK {
			t.Errorf("cells_per_line=%d: got status %d (%s), want 200", width, w.Code, w.Body.String())
		}
	}
}

// TestLayoutInvalidLineWidth422 rejects missing, non-integer, and
// out-of-range widths, always stating the allowed range and never leaking
// partial lines.
func TestLayoutInvalidLineWidth422(t *testing.T) {
	bodies := map[string]string{
		"missing":        `{"text":"ab"}`,
		"null":           `{"text":"ab","cells_per_line":null}`,
		"below min":      `{"text":"ab","cells_per_line":1}`,
		"zero":           `{"text":"ab","cells_per_line":0}`,
		"negative":       `{"text":"ab","cells_per_line":-5}`,
		"above max":      `{"text":"ab","cells_per_line":81}`,
		"fractional":     `{"text":"ab","cells_per_line":2.5}`,
		"float literal":  `{"text":"ab","cells_per_line":10.0}`,
		"string":         `{"text":"ab","cells_per_line":"10"}`,
		"boolean":        `{"text":"ab","cells_per_line":true}`,
		"array":          `{"text":"ab","cells_per_line":[10]}`,
		"object":         `{"text":"ab","cells_per_line":{}}`,
		"huge overflow":  `{"text":"ab","cells_per_line":99999999999999999999}`,
		"exponent float": `{"text":"ab","cells_per_line":1e1}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			w := postLayout(t, body)
			assertLayoutError(t, w, http.StatusUnprocessableEntity, "invalid_line_width")
			var env struct {
				Err struct {
					Min int `json:"min"`
					Max int `json:"max"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("cannot decode error envelope: %v", err)
			}
			if env.Err.Min != 2 || env.Err.Max != 80 {
				t.Errorf("got range %d-%d, want 2-80", env.Err.Min, env.Err.Max)
			}
		})
	}
}

// TestLayoutTextErrorsReuseEncodeCodes: text problems on /layout return
// the exact /encode errors and never leak partial lines.
func TestLayoutTextErrorsReuseEncodeCodes(t *testing.T) {
	t.Run("empty text", func(t *testing.T) {
		w := postLayout(t, `{"text":"","cells_per_line":10}`)
		assertLayoutError(t, w, http.StatusUnprocessableEntity, "empty_text")
	})
	t.Run("invalid character", func(t *testing.T) {
		w := postLayout(t, `{"text":"ab汉","cells_per_line":10}`)
		assertLayoutError(t, w, http.StatusUnprocessableEntity, "invalid_character")
	})
	t.Run("too long", func(t *testing.T) {
		w := postLayout(t, `{"text":"`+strings.Repeat("a", 2001)+`","cells_per_line":10}`)
		assertLayoutError(t, w, http.StatusUnprocessableEntity, "text_too_long")
	})
}

// TestLayoutTextErrorBeatsWidthError: encoding runs first, so a text
// problem surfaces even when the width is also invalid.
func TestLayoutTextErrorBeatsWidthError(t *testing.T) {
	w := postLayout(t, `{"text":"","cells_per_line":1}`)
	assertLayoutError(t, w, http.StatusUnprocessableEntity, "empty_text")

	w = postLayout(t, `{"text":"ab汉"}`)
	assertLayoutError(t, w, http.StatusUnprocessableEntity, "invalid_character")
}

func TestLayoutMalformedJSON400(t *testing.T) {
	for _, body := range []string{`{not json`, `{"text":42,"cells_per_line":10}`, `["a"]`} {
		w := postLayout(t, body)
		assertLayoutError(t, w, http.StatusBadRequest, "bad_request")
	}
}

// assertLayoutError mirrors assertError for /layout: status, code, and no
// partial "lines" field in the body.
func assertLayoutError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("got status %d (%s), want %d", w.Code, w.Body.String(), wantStatus)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if _, leaked := raw["lines"]; leaked {
		t.Errorf("error response must not contain partial lines: %s", w.Body.String())
	}
	var env struct {
		Err struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("cannot decode error envelope: %v", err)
	}
	if env.Err.Code != wantCode {
		t.Errorf("got code %q, want %q", env.Err.Code, wantCode)
	}
}

// assertError verifies the status, the error code, the optional
// source_index, and that no partial items were returned.
func assertError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string, wantIndex *int) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("got status %d (%s), want %d", w.Code, w.Body.String(), wantStatus)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if _, leaked := raw["items"]; leaked {
		t.Errorf("error response must not contain partial items: %s", w.Body.String())
	}
	var env struct {
		Err struct {
			Code        string `json:"code"`
			SourceIndex *int   `json:"source_index"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("cannot decode error envelope: %v", err)
	}
	if env.Err.Code != wantCode {
		t.Errorf("got code %q, want %q", env.Err.Code, wantCode)
	}
	if wantIndex != nil {
		if env.Err.SourceIndex == nil || *env.Err.SourceIndex != *wantIndex {
			t.Errorf("got source_index %v, want %d", env.Err.SourceIndex, *wantIndex)
		}
	}
}

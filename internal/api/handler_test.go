package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type proofreadDiscrepancy struct {
	Category       string `json:"category"`
	SourceIndex    *int   `json:"source_index"`
	Expected       []int  `json:"expected"`
	Observed       []int  `json:"observed"`
	ReadbackOffset *int   `json:"readback_offset"`
}

type proofreadResponse struct {
	Matched       int                    `json:"matched"`
	EditCount     int                    `json:"edit_count"`
	Discrepancies []proofreadDiscrepancy `json:"discrepancies"`
}

func postProofread(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/proofread", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	NewRouter().ServeHTTP(w, req)
	return w
}

func decodeProofread(t *testing.T, w *httptest.ResponseRecorder) proofreadResponse {
	t.Helper()
	var resp proofreadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, w.Body.String())
	}
	return resp
}

// TestProofreadPerfectBody pins the exact body of a readback that matches
// the expected stream exactly (newline skipped).
func TestProofreadPerfectBody(t *testing.T) {
	// "A\nB" encodes to [1], newline, [3]; readback cells ignore the line.
	w := postProofread(t, `{"text":"A\nB","observed_cells":[1,3]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"matched":2,"edit_count":0,"discrepancies":[]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

// TestProofreadDroppedPrefixBody pins the named acceptance scenario: a
// dropped capital prefix (one-cell change) beside a body substitution
// (two-cell change), discrepancies ordered along the path.
func TestProofreadDroppedPrefixBody(t *testing.T) {
	// "ab": a [32,1], b [32,3]; readback [1,32,7].
	w := postProofread(t, `{"text":"ab","observed_cells":[1,32,7]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"matched":0,"edit_count":2,"discrepancies":[` +
		`{"category":"single_change","source_index":0,"expected":[32,1],"observed":[1],"readback_offset":0},` +
		`{"category":"double_change","source_index":1,"expected":[32,3],"observed":[32,7],"readback_offset":1}` +
		`]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

// TestProofreadEmptyReadbackAllMissingBody pins an empty readback: every
// non-newline record is missing, ordered by source_index, with no offsets.
func TestProofreadEmptyReadbackAllMissingBody(t *testing.T) {
	w := postProofread(t, `{"text":"Ab\n12","observed_cells":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"matched":0,"edit_count":6,"discrepancies":[` +
		`{"category":"missing","source_index":0,"expected":[1],"observed":[]},` +
		`{"category":"missing","source_index":1,"expected":[32,3],"observed":[]},` +
		`{"category":"missing","source_index":3,"expected":[60,1],"observed":[]},` +
		`{"category":"missing","source_index":4,"expected":[3],"observed":[]}` +
		`]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

// TestProofreadUnexpectedBody pins stray embossing: no source_index,
// expected null, readback offset reported.
func TestProofreadUnexpectedBody(t *testing.T) {
	w := postProofread(t, `{"text":"A","observed_cells":[9,1]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"matched":1,"edit_count":1,"discrepancies":[` +
		`{"category":"unexpected","expected":null,"observed":[9],"readback_offset":0}` +
		`]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

// TestProofreadDoubleChangeBody pins an atom absorbing two readback cells.
func TestProofreadDoubleChangeBody(t *testing.T) {
	w := postProofread(t, `{"text":"A","observed_cells":[32,7]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, body %s", w.Code, w.Body.String())
	}
	want := `{"matched":0,"edit_count":2,"discrepancies":[` +
		`{"category":"double_change","source_index":0,"expected":[1],"observed":[32,7],"readback_offset":0}` +
		`]}`
	if w.Body.String() != want {
		t.Errorf("got\n%s\nwant\n%s", w.Body.String(), want)
	}
}

// TestProofreadDiscrepanciesPathOrdered checks discrepancies follow the
// path order and that readback offsets index the readback stream. "A"
// expects [1]; readback [9,1,8] aligns as an unexpected cell, a perfect
// match, and another unexpected cell — the alternative where the atom
// absorbs two cells ties on edits but loses on matched count.
func TestProofreadDiscrepanciesPathOrdered(t *testing.T) {
	w := postProofread(t, `{"text":"A","observed_cells":[9,1,8]}`)
	resp := decodeProofread(t, w)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if resp.Matched != 1 || resp.EditCount != 2 || len(resp.Discrepancies) != 2 {
		t.Fatalf("got %+v", resp)
	}
	d0 := resp.Discrepancies[0]
	if d0.Category != "unexpected" || d0.SourceIndex != nil ||
		d0.Expected != nil || !reflect.DeepEqual(d0.Observed, []int{9}) ||
		d0.ReadbackOffset == nil || *d0.ReadbackOffset != 0 {
		t.Errorf("d0 = %+v", d0)
	}
	d1 := resp.Discrepancies[1]
	if d1.Category != "unexpected" ||
		!reflect.DeepEqual(d1.Observed, []int{8}) ||
		d1.ReadbackOffset == nil || *d1.ReadbackOffset != 2 {
		t.Errorf("d1 = %+v", d1)
	}
}

// TestProofreadBoundaryCellValues accepts exactly 0 and 63.
func TestProofreadBoundaryCellValues(t *testing.T) {
	w := postProofread(t, `{"text":"  ","observed_cells":[0,63]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	resp := decodeProofread(t, w)
	if resp.Matched != 1 || resp.EditCount != 1 || len(resp.Discrepancies) != 1 {
		t.Fatalf("got %+v", resp)
	}
}

// TestProofreadEmptyValuesAre422 distinguishes empty values (semantic
// 422) from type errors (400): empty/null text and a missing/null
// observed_cells are empty values; an empty array is a legal (if
// content-free) readback and aligns with everything missing.
func TestProofreadEmptyValuesAre422(t *testing.T) {
	cases := map[string]string{
		"empty text":       `{"text":"","observed_cells":[1]}`,
		"null text":        `{"text":null,"observed_cells":[1]}`,
		"missing text":     `{"observed_cells":[1]}`,
		"missing observed": `{"text":"A"}`,
		"null observed":    `{"text":"A","observed_cells":null}`,
		"both empty":       `{"text":"","observed_cells":[]}`,
		"both missing":     `{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := postProofread(t, body)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("got %d %s, want 422", w.Code, w.Body.String())
			}
		})
	}
}

// TestProofreadEmptyArrayIsLegalReadback: observed_cells [] is not a
// missing field — it is a present, empty readback and yields the
// all-missing alignment (200), not a 422.
func TestProofreadEmptyArrayIsLegalReadback(t *testing.T) {
	w := postProofread(t, `{"text":"A","observed_cells":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200", w.Code, w.Body.String())
	}
	resp := decodeProofread(t, w)
	if resp.Matched != 0 || resp.EditCount != 1 || len(resp.Discrepancies) != 1 ||
		resp.Discrepancies[0].Category != "missing" {
		t.Fatalf("got %+v", resp)
	}
}

// TestProofreadTrailingSecondSegment400: a valid object followed by a
// second JSON value is a malformed whole request, never a partial
// proofread result.
func TestProofreadTrailingSecondSegment400(t *testing.T) {
	bodies := map[string]string{
		"second object":  `{"text":"A","observed_cells":[1]}{}`,
		"trailing array": `{"text":"A","observed_cells":[1]}[]`,
		"trailing token": `{"text":"A","observed_cells":[1]}true`,
		"trailing junk":  `{"text":"A","observed_cells":[1]}xyz`,
		"two numbers":    `{"text":"A","observed_cells":[1]}42`,
		"duplicate body": `{"text":"A","observed_cells":[1]}{"text":"B","observed_cells":[3]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			w := postProofread(t, body)
			assertProofreadError(t, w, http.StatusBadRequest, "bad_request")
		})
	}
}

// TestProofreadMalformedJSON400 rejects bodies that are not a legal JSON
// object with the right field types.
func TestProofreadMalformedJSON400(t *testing.T) {
	bodies := map[string]string{
		"malformed":          `{not json`,
		"truncated":          `{"text":"A","observed_cells":`,
		"array body":         `["A"]`,
		"text non-string":    `{"text":42,"observed_cells":[1]}`,
		"text boolean":       `{"text":true,"observed_cells":[1]}`,
		"cells string":       `{"text":"A","observed_cells":"[1]"}`,
		"cells number":       `{"text":"A","observed_cells":1}`,
		"cells object":       `{"text":"A","observed_cells":{}}`,
		"cells null element": `{"text":"A","observed_cells":[1,null]}`,
		"cells string elem":  `{"text":"A","observed_cells":["1"]}`,
		"cells bool elem":    `{"text":"A","observed_cells":[true]}`,
		"cells object elem":  `{"text":"A","observed_cells":[{}]}`,
		"cells array elem":   `{"text":"A","observed_cells":[[1]]}`,
		"cells fraction":     `{"text":"A","observed_cells":[1.5]}`,
		"cells exponent":     `{"text":"A","observed_cells":[1e1]}`,
		"cells float tail":   `{"text":"A","observed_cells":[10.0]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			w := postProofread(t, body)
			assertProofreadError(t, w, http.StatusBadRequest, "bad_request")
		})
	}
}

// TestProofreadTextValidation422 reuses the /encode contract and never
// leaks a partial alignment.
func TestProofreadTextValidation422(t *testing.T) {
	t.Run("empty text", func(t *testing.T) {
		w := postProofread(t, `{"text":"","observed_cells":[1]}`)
		assertProofreadError(t, w, http.StatusUnprocessableEntity, "empty_text")
	})
	t.Run("invalid character", func(t *testing.T) {
		w := postProofread(t, `{"text":"ab汉","observed_cells":[1]}`)
		assertProofreadError(t, w, http.StatusUnprocessableEntity, "invalid_character")
	})
	t.Run("too long", func(t *testing.T) {
		body := `{"text":"` + strings.Repeat("a", 2001) + `","observed_cells":[1]}`
		w := postProofread(t, body)
		assertProofreadError(t, w, http.StatusUnprocessableEntity, "text_too_long")
	})
}

// TestProofreadObservedValidation422 checks the missing/overlong/value
// precedence and the reported index/value, with no partial alignment.
func TestProofreadObservedValidation422(t *testing.T) {
	t.Run("missing field", func(t *testing.T) {
		w := postProofread(t, `{"text":"A"}`)
		assertProofreadError(t, w, http.StatusUnprocessableEntity, "invalid_observed_cells")
	})
	t.Run("null field", func(t *testing.T) {
		w := postProofread(t, `{"text":"A","observed_cells":null}`)
		assertProofreadError(t, w, http.StatusUnprocessableEntity, "invalid_observed_cells")
	})
	t.Run("above 63", func(t *testing.T) {
		w := postProofread(t, `{"text":"A","observed_cells":[1,64,3]}`)
		assertProofreadCellError(t, w, 1, 64)
	})
	t.Run("negative", func(t *testing.T) {
		w := postProofread(t, `{"text":"A","observed_cells":[-1]}`)
		assertProofreadCellError(t, w, 0, -1)
	})
	t.Run("huge integer is a value error", func(t *testing.T) {
		w := postProofread(t, `{"text":"A","observed_cells":[99999999999999999999999]}`)
		assertProofreadError(t, w, http.StatusUnprocessableEntity, "invalid_observed_cells")
		var env struct {
			Err struct {
				Index *int `json:"index"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Err.Index == nil || *env.Err.Index != 0 {
			t.Fatalf("huge integer must be reported at index 0, got %+v", env.Err)
		}
	})
	t.Run("first illegal index wins", func(t *testing.T) {
		w := postProofread(t, `{"text":"A","observed_cells":[1,2,100,64]}`)
		assertProofreadCellError(t, w, 2, 100)
	})
	t.Run("overlong beats illegal value", func(t *testing.T) {
		body := `{"text":"A","observed_cells":[` +
			strings.Repeat("0,", 4000) + `64]}`
		w := postProofread(t, body)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		var env struct {
			Err struct {
				Code   string `json:"code"`
				Length int    `json:"length"`
				Limit  int    `json:"limit"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Err.Code != "invalid_observed_cells" || env.Err.Length != 4001 || env.Err.Limit != 4000 {
			t.Fatalf("got %+v", env.Err)
		}
	})
	t.Run("exactly 4000 accepted", func(t *testing.T) {
		body := `{"text":"` + strings.Repeat("a", 2000) +
			`","observed_cells":[` + strings.Repeat("0,", 3999) + `0]}`
		w := postProofread(t, body)
		if w.Code != http.StatusOK {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
}

// TestProofreadValidationPrecedence: a well-formed object validates text
// before the readback; an ill-formed object is a 400 regardless of text.
func TestProofreadValidationPrecedence(t *testing.T) {
	w := postProofread(t, `{"text":"","observed_cells":[]}`)
	assertProofreadError(t, w, http.StatusUnprocessableEntity, "empty_text")

	w = postProofread(t, `{"text":"ab汉","observed_cells":[64]}`)
	assertProofreadError(t, w, http.StatusUnprocessableEntity, "invalid_character")

	w = postProofread(t, `{"text":"","observed_cells":"x"}`)
	assertProofreadError(t, w, http.StatusBadRequest, "bad_request")
}

// TestProofreadDeterministic replays an alignment and requires
// byte-identical bodies.
func TestProofreadDeterministic(t *testing.T) {
	body := `{"text":"aBc 12\nZz","observed_cells":[32,1,9,0,60,1,7,53,32,7]}`
	w1 := postProofread(t, body)
	w2 := postProofread(t, body)
	if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
		t.Fatalf("got %d/%d", w1.Code, w2.Code)
	}
	if w1.Body.String() != w2.Body.String() {
		t.Fatalf("bodies differ:\n%s\n%s", w1.Body.String(), w2.Body.String())
	}
}

// TestProofreadMethodRouting confirms /proofread only answers POST; the
// router does not enable method-not-allowed handling, so a GET is a 404.
func TestProofreadMethodRouting(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/proofread", nil)
	NewRouter().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /proofread: got %d, want 404", w.Code)
	}
}

// TestOldEndpointsStillServed is a regression guard that the new route
// leaves /encode, /layout and /healthz untouched.
func TestOldEndpointsStillServed(t *testing.T) {
	if w := postEncode(t, `{"text":"A1b 23\nZz0"}`); w.Code != http.StatusOK {
		t.Errorf("/encode got %d", w.Code)
	}
	if w := postLayout(t, `{"text":"Ab\n12","cells_per_line":2}`); w.Code != http.StatusOK {
		t.Errorf("/layout got %d", w.Code)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	NewRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/healthz got %d", w.Code)
	}
}

// assertProofreadError verifies status, code, and that no partial
// alignment leaked into an error body.
func assertProofreadError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("got status %d (%s), want %d", w.Code, w.Body.String(), wantStatus)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	for _, leaked := range []string{"matched", "edit_count", "discrepancies"} {
		if _, ok := raw[leaked]; ok {
			t.Errorf("error response must not contain partial %q: %s", leaked, w.Body.String())
		}
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
		t.Errorf("got code %q, want %q (body %s)", env.Err.Code, wantCode, w.Body.String())
	}
}

// assertProofreadCellError pins the 422 invalid_observed_cells body for
// an out-of-range value: first illegal index and its value reported.
func assertProofreadCellError(t *testing.T, w *httptest.ResponseRecorder, wantIndex, wantValue int) {
	t.Helper()
	assertProofreadError(t, w, http.StatusUnprocessableEntity, "invalid_observed_cells")
	var env struct {
		Err struct {
			Index *int `json:"index"`
			Value int  `json:"value"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Err.Index == nil || *env.Err.Index != wantIndex {
		t.Errorf("got index %v, want %d", env.Err.Index, wantIndex)
	}
	if env.Err.Value != wantValue {
		t.Errorf("got value %d, want %d", env.Err.Value, wantValue)
	}
}

// Command verify runs a one-shot acceptance suite against a live braille
// API instance and exits non-zero if any check fails.
//
// The target is set with API_URL (default http://localhost:8080).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"
)

type item struct {
	SourceIndex int    `json:"source_index"`
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	PrefixType  string `json:"prefix_type"`
	Cells       []int  `json:"cells"`
}

type line struct {
	LineIndex int    `json:"line_index"`
	Items     []item `json:"items"`
}

type discrepancy struct {
	Category       string `json:"category"`
	SourceIndex    *int   `json:"source_index"`
	Expected       []int  `json:"expected"`
	Observed       []int  `json:"observed"`
	ReadbackOffset *int   `json:"readback_offset"`
}

type proofreadResponse struct {
	Matched       int           `json:"matched"`
	EditCount     int           `json:"edit_count"`
	Discrepancies []discrepancy `json:"discrepancies"`
}

type errorBody struct {
	Code        string `json:"code"`
	SourceIndex *int   `json:"source_index"`
	Source      string `json:"source"`
	Length      int    `json:"length"`
	Limit       int    `json:"limit"`
	Min         int    `json:"min"`
	Max         int    `json:"max"`
	Index       *int   `json:"index"`
	Value       int    `json:"value"`
}

var (
	baseURL  = "http://localhost:8080"
	failures int
)

func main() {
	if v := os.Getenv("API_URL"); v != "" {
		baseURL = strings.TrimRight(v, "/")
	}
	waitReady()

	checkHealthz()
	checkEncodeGolden()
	checkNumberSegmentState()
	checkDigitMapping()
	checkEmptyText()
	checkTooLong()
	checkBoundaryExact()
	checkInvalidCharacters()
	checkMalformedJSON()
	checkLayoutGolden()
	checkLayoutNumberRunAcrossLines()
	checkLayoutConsecutiveNewlines()
	checkLayoutTrailingNewline()
	checkLayoutLineWidth()
	checkLayoutTextErrors()
	checkLayoutMalformedJSON()
	checkProofreadExact()
	checkProofreadDroppedPrefixWithChange()
	checkProofreadEmptyReadbackAllMissing()
	checkProofreadUnexpected()
	checkProofreadSkipsNewlines()
	checkProofreadValidation()
	checkProofreadMalformedJSON()
	checkDeterministicResponses()

	if failures > 0 {
		fmt.Printf("\n%d check(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Println("\nall checks passed")
}

func waitReady() {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	fail("readiness", "API did not become healthy within 30s")
	os.Exit(1)
}

func checkHealthz() {
	resp, body := do("GET", "/healthz", "")
	pass("healthz", resp.StatusCode == 200 && strings.Contains(string(body), `"status":"ok"`),
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkEncodeGolden verifies the full per-character serialization of a
// mixed input, including capital/number prefixes and the newline record.
func checkEncodeGolden() {
	want := []item{
		{0, "A", "character", "none", []int{1}},
		{1, "1", "character", "number", []int{60, 1}},
		{2, "b", "character", "capital", []int{32, 3}},
		{3, " ", "character", "none", []int{0}},
		{4, "2", "character", "number", []int{60, 3}},
		{5, "3", "character", "none", []int{9}},
		{6, "\n", "newline", "none", []int{}},
		{7, "Z", "character", "none", []int{53}},
		{8, "z", "character", "capital", []int{32, 53}},
		{9, "0", "character", "number", []int{60, 26}},
	}
	resp, body := do("POST", "/encode", `{"text":"A1b 23\nZz0"}`)
	items, ok := decodeItems(body)
	good := resp.StatusCode == 200 && ok && reflect.DeepEqual(items, want) &&
		bytes.Contains(body, []byte(`"cells":[]`))
	pass("encode golden A1b 23\\nZz0", good, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkNumberSegmentState verifies the number indicator is emitted only at
// the head of each consecutive digit run and re-armed after separators.
func checkNumberSegmentState() {
	want := []item{
		{0, "1", "character", "number", []int{60, 1}},
		{1, "2", "character", "none", []int{3}},
		{2, " ", "character", "none", []int{0}},
		{3, "3", "character", "number", []int{60, 9}},
		{4, "\n", "newline", "none", []int{}},
		{5, "4", "character", "number", []int{60, 25}},
		{6, "a", "character", "capital", []int{32, 1}},
		{7, "5", "character", "number", []int{60, 17}},
		{8, "Z", "character", "none", []int{53}},
		{9, "6", "character", "number", []int{60, 11}},
	}
	resp, body := do("POST", "/encode", `{"text":"12 3\n4a5Z6"}`)
	items, ok := decodeItems(body)
	pass("number segment state switching", resp.StatusCode == 200 && ok && reflect.DeepEqual(items, want),
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkDigitMapping verifies 1-9 reuse A-I and 0 reuses J.
func checkDigitMapping() {
	wantCells := [][]int{{60, 1}, {3}, {9}, {25}, {17}, {11}, {27}, {19}, {10}, {26}}
	resp, body := do("POST", "/encode", `{"text":"1234567890"}`)
	items, ok := decodeItems(body)
	good := resp.StatusCode == 200 && ok && len(items) == 10
	if good {
		for i, it := range items {
			if !reflect.DeepEqual(it.Cells, wantCells[i]) {
				good = false
				break
			}
		}
	}
	pass("digits reuse J,A-I masks", good, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

func checkEmptyText() {
	resp, body := do("POST", "/encode", `{"text":""}`)
	eb, hasItems := decodeError(body)
	pass("empty text -> 422 empty_text",
		resp.StatusCode == 422 && eb.Code == "empty_text" && !hasItems,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

func checkTooLong() {
	resp, body := do("POST", "/encode", `{"text":"`+strings.Repeat("a", 2001)+`"}`)
	eb, hasItems := decodeError(body)
	pass("2001 code points -> 422 text_too_long",
		resp.StatusCode == 422 && eb.Code == "text_too_long" && eb.Length == 2001 && eb.Limit == 2000 && !hasItems,
		fmt.Sprintf("got %d (body truncated) %.120s", resp.StatusCode, body))
}

func checkBoundaryExact() {
	resp, body := do("POST", "/encode", `{"text":"`+strings.Repeat("a", 2000)+`"}`)
	items, ok := decodeItems(body)
	pass("exactly 2000 code points -> 200",
		resp.StatusCode == 200 && ok && len(items) == 2000,
		fmt.Sprintf("got %d, items=%d", resp.StatusCode, len(items)))
}

// checkInvalidCharacters verifies whole-request 422 rejection with the
// first illegal code point index reported and no partial encoding.
func checkInvalidCharacters() {
	cases := []struct {
		name      string
		text      string
		wantIndex int
	}{
		{"hanzi", "ab汉cd", 2},
		{"tab", "a\tb", 1},
		{"carriage return", "a\rb", 1},
		{"punctuation", "ab,cd", 2},
		{"fullwidth", "Ａbc", 0},
		{"hanzi after ascii", "abc汉", 3},
	}
	for _, tc := range cases {
		payload, _ := json.Marshal(map[string]string{"text": tc.text})
		resp, body := do("POST", "/encode", string(payload))
		eb, hasItems := decodeError(body)
		good := resp.StatusCode == 422 && eb.Code == "invalid_character" &&
			eb.SourceIndex != nil && *eb.SourceIndex == tc.wantIndex && !hasItems
		pass("invalid "+tc.name+" -> 422", good, fmt.Sprintf("got %d %s", resp.StatusCode, body))
	}
}

func checkMalformedJSON() {
	resp, body := do("POST", "/encode", `{not json`)
	pass("malformed JSON -> 400", resp.StatusCode == 400, fmt.Sprintf("got %d %s", resp.StatusCode, body))

	resp, body = do("POST", "/encode", `{"text": 42}`)
	pass("non-string text -> 400", resp.StatusCode == 400, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkLayoutGolden verifies a mixed preview at width 2: the double-cell
// "b" wraps unsplit, the explicit newline ends line 1, and the number run
// "12" splits across lines 2-3.
func checkLayoutGolden() {
	want := []line{
		{0, []item{{0, "A", "character", "none", []int{1}}}},
		{1, []item{
			{1, "b", "character", "capital", []int{32, 3}},
			{2, "\n", "newline", "none", []int{}},
		}},
		{2, []item{{3, "1", "character", "number", []int{60, 1}}}},
		{3, []item{{4, "2", "character", "none", []int{3}}}},
	}
	resp, body := do("POST", "/layout", `{"text":"Ab\n12","cells_per_line":2}`)
	lines, ok := decodeLines(body)
	pass("layout golden Ab\\n12 @2", resp.StatusCode == 200 && ok && reflect.DeepEqual(lines, want),
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkLayoutNumberRunAcrossLines verifies an automatic wrap inside a
// number run keeps the continuation record unchanged: no fresh number
// indicator, same source_index.
func checkLayoutNumberRunAcrossLines() {
	want := []line{
		{0, []item{{0, "1", "character", "number", []int{60, 1}}}},
		{1, []item{{1, "2", "character", "none", []int{3}}}},
	}
	resp, body := do("POST", "/layout", `{"text":"12","cells_per_line":2}`)
	lines, ok := decodeLines(body)
	pass("number run continues across auto wrap", resp.StatusCode == 200 && ok && reflect.DeepEqual(lines, want),
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkLayoutConsecutiveNewlines verifies a run of newlines leaves a
// visible empty line between content lines.
func checkLayoutConsecutiveNewlines() {
	want := []line{
		{0, []item{
			{0, "a", "character", "capital", []int{32, 1}},
			{1, "\n", "newline", "none", []int{}},
		}},
		{1, []item{
			{2, "\n", "newline", "none", []int{}},
		}},
		{2, []item{
			{3, "b", "character", "capital", []int{32, 3}},
		}},
	}
	resp, body := do("POST", "/layout", `{"text":"a\n\nb","cells_per_line":10}`)
	lines, ok := decodeLines(body)
	pass("consecutive newlines keep empty line", resp.StatusCode == 200 && ok && reflect.DeepEqual(lines, want),
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkLayoutTrailingNewline verifies a trailing newline leaves a visible
// empty final line serialized with "items":[].
func checkLayoutTrailingNewline() {
	want := []line{
		{0, []item{
			{0, "a", "character", "capital", []int{32, 1}},
			{1, "\n", "newline", "none", []int{}},
		}},
		{1, []item{}},
	}
	resp, body := do("POST", "/layout", `{"text":"a\n","cells_per_line":10}`)
	lines, ok := decodeLines(body)
	good := resp.StatusCode == 200 && ok && reflect.DeepEqual(lines, want) &&
		bytes.Contains(body, []byte(`"items":[]`))
	pass("trailing newline keeps empty line", good, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkLayoutLineWidth verifies the accepted extremes and the 422
// invalid_line_width rejection (with the allowed range stated) for
// missing, non-integer, and out-of-range widths.
func checkLayoutLineWidth() {
	for _, w := range []int{2, 80} {
		payload := fmt.Sprintf(`{"text":"ab","cells_per_line":%d}`, w)
		resp, body := do("POST", "/layout", payload)
		_, ok := decodeLines(body)
		pass(fmt.Sprintf("cells_per_line=%d -> 200", w), resp.StatusCode == 200 && ok,
			fmt.Sprintf("got %d %s", resp.StatusCode, body))
	}

	bad := map[string]string{
		"missing":     `{"text":"ab"}`,
		"below min":   `{"text":"ab","cells_per_line":1}`,
		"above max":   `{"text":"ab","cells_per_line":81}`,
		"fractional":  `{"text":"ab","cells_per_line":2.5}`,
		"string":      `{"text":"ab","cells_per_line":"10"}`,
		"null":        `{"text":"ab","cells_per_line":null}`,
		"zero":        `{"text":"ab","cells_per_line":0}`,
		"negative":    `{"text":"ab","cells_per_line":-3}`,
		"boolean":     `{"text":"ab","cells_per_line":true}`,
		"float 10.0":  `{"text":"ab","cells_per_line":10.0}`,
		"huge number": `{"text":"ab","cells_per_line":99999999999999999999}`,
	}
	for name, payload := range bad {
		resp, body := do("POST", "/layout", payload)
		eb, hasLines := decodeLayoutError(body)
		good := resp.StatusCode == 422 && eb.Code == "invalid_line_width" &&
			eb.Min == 2 && eb.Max == 80 && !hasLines
		pass("layout width "+name+" -> 422 invalid_line_width", good,
			fmt.Sprintf("got %d %s", resp.StatusCode, body))
	}
}

// checkLayoutTextErrors verifies text problems on /layout return the
// existing /encode error codes and never leak partial lines.
func checkLayoutTextErrors() {
	resp, body := do("POST", "/layout", `{"text":"","cells_per_line":10}`)
	eb, hasLines := decodeLayoutError(body)
	pass("layout empty text -> 422 empty_text",
		resp.StatusCode == 422 && eb.Code == "empty_text" && !hasLines,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))

	resp, body = do("POST", "/layout", `{"text":"ab汉","cells_per_line":10}`)
	eb, hasLines = decodeLayoutError(body)
	pass("layout invalid character -> 422 invalid_character",
		resp.StatusCode == 422 && eb.Code == "invalid_character" &&
			eb.SourceIndex != nil && *eb.SourceIndex == 2 && !hasLines,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))

	resp, body = do("POST", "/layout", `{"text":"`+strings.Repeat("a", 2001)+`","cells_per_line":10}`)
	eb, hasLines = decodeLayoutError(body)
	pass("layout too long -> 422 text_too_long",
		resp.StatusCode == 422 && eb.Code == "text_too_long" && eb.Length == 2001 && eb.Limit == 2000 && !hasLines,
		fmt.Sprintf("got %d (body truncated) %.120s", resp.StatusCode, body))

	// Encoding runs first: a text problem wins over an invalid width.
	resp, body = do("POST", "/layout", `{"text":"","cells_per_line":1}`)
	eb, hasLines = decodeLayoutError(body)
	pass("layout text error beats width error",
		resp.StatusCode == 422 && eb.Code == "empty_text" && !hasLines,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

func checkLayoutMalformedJSON() {
	resp, body := do("POST", "/layout", `{not json`)
	pass("layout malformed JSON -> 400", resp.StatusCode == 400, fmt.Sprintf("got %d %s", resp.StatusCode, body))

	resp, body = do("POST", "/layout", `{"text":"ab","cells_per_line":`)
	pass("layout truncated JSON -> 400", resp.StatusCode == 400, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkProofreadExact verifies a readback identical to the encoded stream
// (newline skipped) reports every atom matched with zero edits and no
// discrepancies.
func checkProofreadExact() {
	resp, body := do("POST", "/proofread", `{"text":"A1b 23\nZz0","observed_cells":[1,60,1,32,3,0,60,3,9,53,32,53,60,26]}`)
	pr, ok := decodeProofread(body)
	good := resp.StatusCode == 200 && ok &&
		pr.Matched == 9 && pr.EditCount == 0 && len(pr.Discrepancies) == 0 &&
		bytes.Contains(body, []byte(`"discrepancies":[]`))
	pass("proofread exact readback -> all matched", good, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkProofreadDroppedPrefixWithChange verifies the named scenario: a
// dropped capital prefix is a single-cell change (one deletion), while the
// next atom keeps its indicator but has its body substituted (a
// double-cell change). Discrepancies follow the path with readback offsets.
func checkProofreadDroppedPrefixWithChange() {
	resp, body := do("POST", "/proofread", `{"text":"ab","observed_cells":[1,32,7]}`)
	pr, ok := decodeProofread(body)
	want := []discrepancy{
		{
			Category:       "single_change",
			SourceIndex:    intPtr(0),
			Expected:       []int{32, 1},
			Observed:       []int{1},
			ReadbackOffset: intPtr(0),
		},
		{
			Category:       "double_change",
			SourceIndex:    intPtr(1),
			Expected:       []int{32, 3},
			Observed:       []int{32, 7},
			ReadbackOffset: intPtr(1),
		},
	}
	good := resp.StatusCode == 200 && ok &&
		pr.Matched == 0 && pr.EditCount == 2 && reflect.DeepEqual(pr.Discrepancies, want)
	pass("proofread dropped prefix beside substitution", good, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkProofreadEmptyReadbackAllMissing verifies an empty readback reports
// every non-newline record missing (one deletion per expected cell), with
// source indexes but no readback offsets, and the newline record skipped.
func checkProofreadEmptyReadbackAllMissing() {
	resp, body := do("POST", "/proofread", `{"text":"Ab\n12","observed_cells":[]}`)
	pr, ok := decodeProofread(body)
	want := []discrepancy{
		{Category: "missing", SourceIndex: intPtr(0), Expected: []int{1}, Observed: []int{}},
		{Category: "missing", SourceIndex: intPtr(1), Expected: []int{32, 3}, Observed: []int{}},
		{Category: "missing", SourceIndex: intPtr(3), Expected: []int{60, 1}, Observed: []int{}},
		{Category: "missing", SourceIndex: intPtr(4), Expected: []int{3}, Observed: []int{}},
	}
	good := resp.StatusCode == 200 && ok &&
		pr.Matched == 0 && pr.EditCount == 6 && reflect.DeepEqual(pr.Discrepancies, want)
	for _, d := range pr.Discrepancies {
		if d.ReadbackOffset != nil {
			good = false
		}
	}
	pass("proofread empty readback -> all missing", good, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkProofreadUnexpected verifies stray embossing is reported with no
// source index, a null expected list, and the correct readback offset.
func checkProofreadUnexpected() {
	resp, body := do("POST", "/proofread", `{"text":"A","observed_cells":[9,1,8]}`)
	pr, ok := decodeProofread(body)
	want := []discrepancy{
		{Category: "unexpected", Expected: nil, Observed: []int{9}, ReadbackOffset: intPtr(0)},
		{Category: "unexpected", Expected: nil, Observed: []int{8}, ReadbackOffset: intPtr(2)},
	}
	good := resp.StatusCode == 200 && ok &&
		pr.Matched == 1 && pr.EditCount == 2 && reflect.DeepEqual(pr.Discrepancies, want)
	pass("proofread unexpected embossing with offsets", good,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkProofreadSkipsNewlines verifies newline records produce no atoms
// and no discrepancies.
func checkProofreadSkipsNewlines() {
	resp, body := do("POST", "/proofread", `{"text":"A\nB\n","observed_cells":[1,3]}`)
	pr, ok := decodeProofread(body)
	good := resp.StatusCode == 200 && ok &&
		pr.Matched == 2 && pr.EditCount == 0 && len(pr.Discrepancies) == 0
	pass("proofread skips newline records", good, fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkProofreadValidation verifies the validation contract: text first,
// then the observed_cells precedence (missing, overlong, first illegal
// index), and the 0/63 boundaries.
func checkProofreadValidation() {
	// Text errors reuse the /encode codes and win over readback errors.
	resp, body := do("POST", "/proofread", `{"text":"","observed_cells":[]}`)
	eb, leak := decodeProofreadError(body)
	pass("proofread empty text -> 422 empty_text",
		resp.StatusCode == 422 && eb.Code == "empty_text" && !leak,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))

	resp, body = do("POST", "/proofread", `{"text":"ab汉","observed_cells":[64]}`)
	eb, leak = decodeProofreadError(body)
	pass("proofread invalid char beats observed error",
		resp.StatusCode == 422 && eb.Code == "invalid_character" &&
			eb.SourceIndex != nil && *eb.SourceIndex == 2 && !leak,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))

	// Missing observed_cells field -> 422 invalid_observed_cells.
	resp, body = do("POST", "/proofread", `{"text":"A"}`)
	eb, leak = decodeProofreadError(body)
	pass("proofread missing observed_cells -> 422",
		resp.StatusCode == 422 && eb.Code == "invalid_observed_cells" && !leak,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))

	// Out-of-range value with the first illegal index.
	resp, body = do("POST", "/proofread", `{"text":"A","observed_cells":[1,64]}`)
	eb, leak = decodeProofreadError(body)
	pass("proofread cell 64 -> 422 with index 1",
		resp.StatusCode == 422 && eb.Code == "invalid_observed_cells" &&
			eb.Index != nil && *eb.Index == 1 && eb.Value == 64 && !leak,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))

	resp, body = do("POST", "/proofread", `{"text":"A","observed_cells":[-1]}`)
	eb, _ = decodeProofreadError(body)
	pass("proofread cell -1 -> 422 with index 0",
		resp.StatusCode == 422 && eb.Code == "invalid_observed_cells" &&
			eb.Index != nil && *eb.Index == 0 && eb.Value == -1,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))

	// Overlong beats a later illegal value.
	overlong := `{"text":"A","observed_cells":[` + strings.Repeat("0,", 4000) + `64]}`
	resp, body = do("POST", "/proofread", overlong)
	eb, leak = decodeProofreadError(body)
	pass("proofread overlong observed_cells -> 422 length beats value",
		resp.StatusCode == 422 && eb.Code == "invalid_observed_cells" &&
			eb.Length == 4001 && eb.Limit == 4000 && eb.Index == nil && !leak,
		fmt.Sprintf("got %d (body truncated) %.120s", resp.StatusCode, body))

	// Boundary values 0 and 63 are legal.
	resp, body = do("POST", "/proofread", `{"text":"  ","observed_cells":[0,63]}`)
	pass("proofread cell boundaries 0 and 63 -> 200", resp.StatusCode == 200,
		fmt.Sprintf("got %d %s", resp.StatusCode, body))
}

// checkProofreadMalformedJSON verifies malformed JSON and field-type
// errors are 400, distinct from the 422 semantic validations.
func checkProofreadMalformedJSON() {
	cases := map[string]string{
		"malformed":       `{not json`,
		"text nonstring":  `{"text":42,"observed_cells":[1]}`,
		"cells string":    `{"text":"A","observed_cells":"[1]"}`,
		"cells scalar":    `{"text":"A","observed_cells":1}`,
		"cells null elem": `{"text":"A","observed_cells":[null]}`,
		"cells fraction":  `{"text":"A","observed_cells":[1.5]}`,
		"cells exponent":  `{"text":"A","observed_cells":[1e1]}`,
	}
	for name, payload := range cases {
		resp, body := do("POST", "/proofread", payload)
		eb, leak := decodeProofreadError(body)
		pass("proofread "+name+" -> 400",
			resp.StatusCode == 400 && eb.Code == "bad_request" && !leak,
			fmt.Sprintf("got %d %s", resp.StatusCode, body))
	}
}

func intPtr(v int) *int { return &v }

// checkDeterministicResponses replays the encode, layout and proofread
// goldens and requires byte-identical bodies.
func checkDeterministicResponses() {
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"encode golden", "/encode", `{"text":"A1b 23\nZz0"}`},
		{"layout golden", "/layout", `{"text":"Ab\n12","cells_per_line":2}`},
		{"proofread golden", "/proofread", `{"text":"ab","observed_cells":[1,32,7]}`},
	} {
		resp1, body1 := do("POST", tc.path, tc.body)
		resp2, body2 := do("POST", tc.path, tc.body)
		good := resp1.StatusCode == 200 && resp2.StatusCode == 200 && bytes.Equal(body1, body2)
		pass(tc.name+" is deterministic", good,
			fmt.Sprintf("status %d/%d, bodies equal: %v", resp1.StatusCode, resp2.StatusCode, bytes.Equal(body1, body2)))
	}
}

func do(method, path, body string) (*http.Response, []byte) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, baseURL+path, rdr)
	if err != nil {
		fail("request build", err.Error())
		return nil, nil
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(method+" "+path, err.Error())
		return nil, nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		fail(method+" "+path, err.Error())
	}
	return resp, data
}

// decodeItems extracts items from a success body.
func decodeItems(body []byte) ([]item, bool) {
	var env struct {
		Items []item `json:"items"`
	}
	if json.Unmarshal(body, &env) != nil || env.Items == nil {
		return nil, false
	}
	for i := range env.Items {
		if env.Items[i].Cells == nil {
			env.Items[i].Cells = []int{}
		}
	}
	return env.Items, true
}

// decodeError extracts the error object and reports whether the body
// leaked a partial "items" field.
func decodeError(body []byte) (errorBody, bool) {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	_, hasItems := raw["items"]
	var env struct {
		Err errorBody `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Err, hasItems
}

// decodeLines extracts lines from a layout success body.
func decodeLines(body []byte) ([]line, bool) {
	var env struct {
		Lines []line `json:"lines"`
	}
	if json.Unmarshal(body, &env) != nil || env.Lines == nil {
		return nil, false
	}
	for i := range env.Lines {
		if env.Lines[i].Items == nil {
			env.Lines[i].Items = []item{}
		}
		for j := range env.Lines[i].Items {
			if env.Lines[i].Items[j].Cells == nil {
				env.Lines[i].Items[j].Cells = []int{}
			}
		}
	}
	return env.Lines, true
}

// decodeLayoutError extracts the error object and reports whether the body
// leaked a partial "lines" field.
func decodeLayoutError(body []byte) (errorBody, bool) {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	_, hasLines := raw["lines"]
	var env struct {
		Err errorBody `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Err, hasLines
}

// decodeProofread extracts the alignment of a proofread success body. It
// reports ok only when the body carries a "matched" field.
func decodeProofread(body []byte) (proofreadResponse, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return proofreadResponse{}, false
	}
	if _, isResult := raw["matched"]; !isResult {
		return proofreadResponse{}, false
	}
	var pr proofreadResponse
	if json.Unmarshal(body, &pr) != nil {
		return proofreadResponse{}, false
	}
	if pr.Discrepancies == nil {
		pr.Discrepancies = []discrepancy{}
	}
	for i := range pr.Discrepancies {
		if pr.Discrepancies[i].Observed == nil {
			pr.Discrepancies[i].Observed = []int{}
		}
	}
	return pr, true
}

// decodeProofreadError extracts the error object and reports whether the
// body leaked a partial proofread result.
func decodeProofreadError(body []byte) (errorBody, bool) {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	_, hasResult := raw["discrepancies"]
	var env struct {
		Err errorBody `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Err, hasResult
}

func pass(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("PASS %s\n", name)
		return
	}
	fail(name, detail)
}

func fail(name, detail string) {
	failures++
	fmt.Printf("FAIL %s: %s\n", name, detail)
}

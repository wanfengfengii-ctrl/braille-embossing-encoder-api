package braille

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// atomsFor encodes text and returns the non-newline atoms the aligner
// uses, so tests can reason in source-record terms.
func atomsFor(t *testing.T, text string) []atom {
	t.Helper()
	items := mustEncode(t, text)
	var atoms []atom
	for _, it := range items {
		if it.Kind == KindNewline {
			continue
		}
		atoms = append(atoms, atom{sourceIndex: it.SourceIndex, cells: it.Cells})
	}
	return atoms
}

func TestProofreadPerfectAlignment(t *testing.T) {
	text := "A1b 23\nZz0"
	// The readback is the encoded cell stream with the newline record
	// skipped.
	observed := []int{1, 60, 1, 32, 3, 0, 60, 3, 9, 53, 32, 53, 60, 26}
	res, err := Proofread(text, ObservedFromInts(observed))
	if err != nil {
		t.Fatalf("Proofread returned error: %+v", err)
	}
	// Nine non-newline source records.
	if res.Matched != 9 || res.EditCount != 0 || len(res.Discrepancies) != 0 {
		t.Fatalf("got %+v", res)
	}
}

// TestProofreadEmptyReadbackAllMissing: with no readback cells every
// non-newline record is missing, charged one deletion per expected cell,
// and newline records produce no discrepancy.
func TestProofreadEmptyReadbackAllMissing(t *testing.T) {
	text := "Ab\n12"
	res, err := Proofread(text, ObservedFromInts([]int{}))
	if err != nil {
		t.Fatalf("Proofread returned error: %+v", err)
	}
	// A [1], b [32,3], 1 [60,1], 2 [3] -> 1+2+2+1 deletions.
	if res.Matched != 0 || res.EditCount != 6 || len(res.Discrepancies) != 4 {
		t.Fatalf("got %+v", res)
	}
	wantCategories := []string{
		DiscrepancyMissing, DiscrepancyMissing,
		DiscrepancyMissing, DiscrepancyMissing,
	}
	wantSource := []int{0, 1, 3, 4} // newline at source_index 2 is skipped
	wantExpected := [][]int{{1}, {32, 3}, {60, 1}, {3}}
	for i, d := range res.Discrepancies {
		if d.Category != wantCategories[i] {
			t.Errorf("discrepancy %d: category %s, want %s", i, d.Category, wantCategories[i])
		}
		if d.SourceIndex == nil || *d.SourceIndex != wantSource[i] {
			t.Errorf("discrepancy %d: source_index %v, want %d", i, d.SourceIndex, wantSource[i])
		}
		if !reflect.DeepEqual(d.Expected, wantExpected[i]) {
			t.Errorf("discrepancy %d: expected %v, want %v", i, d.Expected, wantExpected[i])
		}
		if len(d.Observed) != 0 {
			t.Errorf("discrepancy %d: observed must be empty, got %v", i, d.Observed)
		}
		if d.ReadbackOffset != nil {
			t.Errorf("discrepancy %d: missing atom must carry no offset, got %d", i, *d.ReadbackOffset)
		}
	}
}

// TestProofreadDroppedPrefixWithAdjacentChange pins the named acceptance
// scenario: the capital prefix of "a" was not embossed (a one-cell change
// on its atom), while the following atom "b" kept its indicator but had
// its body substituted (a two-cell change).
func TestProofreadDroppedPrefixWithAdjacentChange(t *testing.T) {
	text := "ab"
	observed := []int{1, 32, 7}
	res, err := Proofread(text, ObservedFromInts(observed))
	if err != nil {
		t.Fatalf("Proofread returned error: %+v", err)
	}
	// a: [32,1] absorbs [1] (prefix dropped, one deletion).
	// b: [32,3] absorbs [32,7] (body substitution).
	if res.Matched != 0 || res.EditCount != 2 || len(res.Discrepancies) != 2 {
		t.Fatalf("got %+v", res)
	}
	d0 := res.Discrepancies[0]
	if d0.Category != DiscrepancySingle || *d0.SourceIndex != 0 ||
		!reflect.DeepEqual(d0.Expected, []int{32, 1}) ||
		!reflect.DeepEqual(d0.Observed, []int{1}) ||
		d0.ReadbackOffset == nil || *d0.ReadbackOffset != 0 {
		t.Errorf("dropped-prefix discrepancy wrong: %+v", d0)
	}
	d1 := res.Discrepancies[1]
	if d1.Category != DiscrepancyDouble || *d1.SourceIndex != 1 ||
		!reflect.DeepEqual(d1.Expected, []int{32, 3}) ||
		!reflect.DeepEqual(d1.Observed, []int{32, 7}) ||
		d1.ReadbackOffset == nil || *d1.ReadbackOffset != 1 {
		t.Errorf("two-cell change discrepancy wrong: %+v", d1)
	}
}

// TestProofreadDoubleChangeIsInsertionAndSubstitution: a one-cell atom
// that reads back as two wrong cells is a double change costing two
// edits (one insertion, one substitution), preferred over a single
// change plus an unexpected cell, which has the same edit count but an
// unexpected cell.
func TestProofreadDoubleChangeIsInsertionAndSubstitution(t *testing.T) {
	text := "A" // atom [1]
	res, err := Proofread(text, ObservedFromInts([]int{32, 7}))
	if err != nil {
		t.Fatalf("Proofread returned error: %+v", err)
	}
	if res.EditCount != 2 || res.Matched != 0 || len(res.Discrepancies) != 1 {
		t.Fatalf("got %+v", res)
	}
	d := res.Discrepancies[0]
	if d.Category != DiscrepancyDouble || *d.SourceIndex != 0 ||
		!reflect.DeepEqual(d.Expected, []int{1}) ||
		!reflect.DeepEqual(d.Observed, []int{32, 7}) ||
		d.ReadbackOffset == nil || *d.ReadbackOffset != 0 {
		t.Errorf("got %+v", d)
	}
}

// TestProofreadUnexpectedCells: stray readback cells are reported as
// unexpected embossing with no source_index, ordered by readback offset.
func TestProofreadUnexpectedCells(t *testing.T) {
	text := "A" // atom [1]
	res, err := Proofread(text, ObservedFromInts([]int{9, 1, 9}))
	if err != nil {
		t.Fatalf("Proofread returned error: %+v", err)
	}
	if res.Matched != 1 || res.EditCount != 2 || len(res.Discrepancies) != 2 {
		t.Fatalf("got %+v", res)
	}
	d0 := res.Discrepancies[0]
	if d0.Category != DiscrepancyUnexpected || d0.SourceIndex != nil ||
		d0.Expected != nil || !reflect.DeepEqual(d0.Observed, []int{9}) ||
		d0.ReadbackOffset == nil || *d0.ReadbackOffset != 0 {
		t.Errorf("first unexpected discrepancy wrong: %+v", d0)
	}
	d1 := res.Discrepancies[1]
	if d1.Category != DiscrepancyUnexpected ||
		!reflect.DeepEqual(d1.Observed, []int{9}) ||
		d1.ReadbackOffset == nil || *d1.ReadbackOffset != 2 {
		t.Errorf("second unexpected discrepancy wrong: %+v", d1)
	}
}

// TestProofreadSubstitutionIsSingleChange: a one-cell atom with one
// differing readback cell is a single change costing one substitution,
// uniquely better than deleting the atom and re-inserting the cell.
func TestProofreadSubstitutionIsSingleChange(t *testing.T) {
	text := "A"
	res, err := Proofread(text, ObservedFromInts([]int{3}))
	if err != nil {
		t.Fatalf("Proofread returned error: %+v", err)
	}
	if res.Matched != 0 || res.EditCount != 1 || len(res.Discrepancies) != 1 {
		t.Fatalf("got %+v", res)
	}
	d := res.Discrepancies[0]
	if d.Category != DiscrepancySingle || *d.SourceIndex != 0 ||
		!reflect.DeepEqual(d.Expected, []int{1}) ||
		!reflect.DeepEqual(d.Observed, []int{3}) ||
		d.ReadbackOffset == nil || *d.ReadbackOffset != 0 {
		t.Errorf("got %+v", d)
	}
}

// TestProofreadSkipsNewlines: a newline carries no cells, so readback
// offsets ignore it entirely and its source index never appears.
func TestProofreadSkipsNewlines(t *testing.T) {
	text := "A\nB"
	res, err := Proofread(text, ObservedFromInts([]int{1, 3}))
	if err != nil {
		t.Fatalf("Proofread returned error: %+v", err)
	}
	if res.Matched != 2 || res.EditCount != 0 || len(res.Discrepancies) != 0 {
		t.Fatalf("got %+v", res)
	}
}

// TestProofreadTieBreakPrefersMatch: with equal edit counts the path
// keeping a perfect match is chosen over one that does not.
func TestProofreadTieBreakPrefersMatch(t *testing.T) {
	// "A" [1], readback [1,9]: matching A plus one unexpected cell (one
	// insertion) ties on edit count with A absorbing [1,9] as a double
	// change (also one insertion); the match path wins on matched count.
	res, err := Proofread("A", ObservedFromInts([]int{1, 9}))
	if err != nil {
		t.Fatalf("Proofread returned error: %+v", err)
	}
	if res.Matched != 1 || res.EditCount != 1 || len(res.Discrepancies) != 1 {
		t.Fatalf("got %+v", res)
	}
	d := res.Discrepancies[0]
	if d.Category != DiscrepancyUnexpected || *d.ReadbackOffset != 1 {
		t.Errorf("expected trailing unexpected cell, got %+v", d)
	}
}

// TestProofreadValidationOrder: text is validated before observed cells,
// then missing field, then overlong, then the first out-of-range index.
func TestProofreadValidationOrder(t *testing.T) {
	// Text problems win over any readback problem.
	if _, err := Proofread("", ObservedFromInts(nil)); err == nil || err.Code != CodeEmptyText {
		t.Fatalf("empty text must win, got %+v", err)
	}
	if _, err := Proofread("ab汉", ObservedFromInts([]int{64})); err == nil || err.Code != CodeInvalidChar {
		t.Fatalf("invalid character must win, got %+v", err)
	}
	if _, err := Proofread("ab", ObservedFromInts(nil)); err == nil || err.Code != CodeInvalidObservedCells {
		t.Fatalf("missing observed_cells, got %+v", err)
	}
	tooLong := make([]int, MaxObservedCells+1)
	if _, err := Proofread("ab", ObservedFromInts(tooLong)); err == nil ||
		err.Code != CodeInvalidObservedCells || err.Length != MaxObservedCells+1 || err.Limit != MaxObservedCells {
		t.Fatalf("overlong observed_cells, got %+v", err)
	}
	// Overlong beats an out-of-range value later in the array.
	tooLong[MaxObservedCells] = 64
	if _, err := Proofread("ab", ObservedFromInts(tooLong)); err == nil || err.Length == 0 {
		t.Fatalf("length must be checked before values, got %+v", err)
	}
	for _, value := range []int{64, -1, 100} {
		if _, err := Proofread("ab", ObservedFromInts([]int{1, value, 3})); err == nil ||
			err.Code != CodeInvalidObservedCells || err.Index == nil || *err.Index != 1 {
			t.Fatalf("value %d must be reported at index 1, got %+v", value, err)
		}
	}
	// Boundary values are accepted by the validator.
	if cells, err := ValidateObserved(ObservedFromInts([]int{0, 63})); err != nil ||
		!reflect.DeepEqual(cells, []int{0, 63}) {
		t.Fatalf("0 and 63 are legal cell values, got %v %+v", cells, err)
	}
}

// TestProofreadDeterministic: the same request always aligns identically.
func TestProofreadDeterministic(t *testing.T) {
	text := "aBc 12\nZz"
	observed := []int{32, 1, 3, 9, 0, 7, 3, 9, 53, 32, 53}
	first, err := Proofread(text, ObservedFromInts(observed))
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 5; k++ {
		got, err := Proofread(text, ObservedFromInts(observed))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("alignment not deterministic:\n%+v\n%+v", got, first)
		}
	}
}

// TestDiscrepancySerialization pins JSON field names and presence rules:
// observed is always an array; expected is null for unexpected cells;
// readback_offset is absent for a missing atom.
func TestDiscrepancySerialization(t *testing.T) {
	off := 2
	idx := 4
	d := Discrepancy{
		Category:       DiscrepancyUnexpected,
		Observed:       []int{9},
		ReadbackOffset: &off,
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"category":"unexpected","expected":null,"observed":[9],"readback_offset":2}`
	if string(raw) != want {
		t.Errorf("got %s, want %s", raw, want)
	}

	missing := Discrepancy{Category: DiscrepancyMissing, SourceIndex: &idx, Expected: []int{32, 1}, Observed: []int{}}
	raw, err = json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	want = `{"category":"missing","source_index":4,"expected":[32,1],"observed":[]}`
	if string(raw) != want {
		t.Errorf("got %s, want %s", raw, want)
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		want, got []int
		dist      int
	}{
		{[]int{1}, []int{1}, 0},
		{[]int{1}, []int{3}, 1},
		{[]int{32, 1}, []int{1}, 1},     // dropped prefix
		{[]int{1}, []int{32, 7}, 2},     // insertion + substitution
		{[]int{32, 3}, []int{32, 7}, 1}, // body substitution
		{[]int{32, 1}, []int{1, 32}, 2}, // reversal needs two substitutions
		{[]int{60, 1}, []int{}, 2},      // both missing
	}
	for _, tc := range cases {
		if d := editDistance(tc.want, tc.got); d != tc.dist {
			t.Errorf("editDistance(%v,%v) = %d, want %d", tc.want, tc.got, d, tc.dist)
		}
	}
}

// TestSolveAlignmentMatchesExhaustiveOracle builds many small random
// instances and compares the DP path against an exhaustive enumeration of
// every feasible path: the chosen path must have the lexicographically
// smallest op sequence among all paths with the optimal (edits, matches,
// unexpected) score.
func TestSolveAlignmentMatchesExhaustiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20260915))
	for iter := 0; iter < 2000; iter++ {
		n := rng.Intn(5) // atoms
		m := rng.Intn(6) // readback cells
		atoms := make([]atom, n)
		for i := range atoms {
			src := i * 2
			if rng.Intn(2) == 0 {
				atoms[i] = atom{sourceIndex: src, cells: []int{rng.Intn(4)}}
			} else {
				atoms[i] = atom{sourceIndex: src, cells: []int{rng.Intn(4), rng.Intn(4)}}
			}
		}
		observed := make([]int, m)
		for i := range observed {
			observed[i] = rng.Intn(4)
		}

		path, dpScore := solveAlignment(atoms, observed)
		ops := make([]op, len(path))
		for i, e := range path {
			ops[i] = e.op
		}

		bestScore, bestOps, found := oracleBest(atoms, observed)
		if !found {
			t.Fatalf("iter %d: oracle found no path", iter)
		}
		if bestOps == nil {
			bestOps = []op{}
		}
		if dpScore != bestScore {
			t.Fatalf("iter %d: atoms=%v observed=%v DP score %+v, oracle %+v",
				iter, atoms, observed, dpScore, bestScore)
		}
		if !reflect.DeepEqual(ops, bestOps) {
			t.Fatalf("iter %d: atoms=%v observed=%v\nDP ops   %v\noracle   %v",
				iter, atoms, observed, ops, bestOps)
		}
	}
}

// oracleBest enumerates every feasible path and returns the best score
// and the lexicographically smallest op sequence achieving it.
func oracleBest(atoms []atom, observed []int) (score, []op, bool) {
	n, m := len(atoms), len(observed)
	var best score
	var bestOps []op
	found := false

	var walk func(i, j int, s score, seq []op)
	walk = func(i, j int, s score, seq []op) {
		if i == n && j == m {
			cand := append([]op(nil), seq...)
			switch {
			case !found:
				best, bestOps, found = s, cand, true
			case scoreLess(s, best):
				best, bestOps = s, cand
			case s == best && opsLess(cand, bestOps):
				bestOps = cand
			}
			return
		}
		// Enumerate in tie-break order; the comparison is order-independent.
		if i < n {
			cells := atoms[i].cells
			k := len(cells)
			if j+k <= m {
				match := true
				for p, want := range cells {
					if observed[j+p] != want {
						match = false
					}
				}
				if match {
					ns := s
					ns.matched++
					walk(i+1, j+k, ns, append(seq, opMatch))
				}
			}
			if j+2 <= m {
				ns := s
				ns.edits += int16(editDistance(atoms[i].cells, observed[j:j+2]))
				walk(i+1, j+2, ns, append(seq, opDouble))
			}
			if j+1 <= m {
				ns := s
				ns.edits += int16(editDistance(atoms[i].cells, observed[j:j+1]))
				walk(i+1, j+1, ns, append(seq, opSingle))
			}
			ns := s
			ns.edits += int16(len(atoms[i].cells))
			walk(i+1, j, ns, append(seq, opMissing))
		}
		if j+1 <= m {
			ns := s
			ns.edits++
			ns.unexpected++
			walk(i, j+1, ns, append(seq, opUnexpected))
		}
	}
	walk(0, 0, score{}, nil)
	return best, bestOps, found
}

// scoreLess mirrors score.better: fewer edits, more matches, fewer
// unexpected cells.
func scoreLess(a, b score) bool {
	if a.edits != b.edits {
		return a.edits < b.edits
	}
	if a.matched != b.matched {
		return a.matched > b.matched
	}
	return a.unexpected < b.unexpected
}

// opsLess compares op sequences lexicographically using the op rank
// (match < double < single < missing < unexpected).
func opsLess(a, b []op) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// TestSolveAlignmentReconstructionConsistency checks the path visits
// feasible nodes, ends at (n,m), and that its per-edge deltas sum to the
// reported totals.
func TestSolveAlignmentReconstructionConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		n, m := 1+rng.Intn(6), rng.Intn(8)
		atoms := make([]atom, n)
		for i := range atoms {
			if rng.Intn(2) == 0 {
				atoms[i] = atom{sourceIndex: i, cells: []int{rng.Intn(5)}}
			} else {
				atoms[i] = atom{sourceIndex: i, cells: []int{rng.Intn(5), rng.Intn(5)}}
			}
		}
		observed := make([]int, m)
		for i := range observed {
			observed[i] = rng.Intn(5)
		}
		path, total := solveAlignment(atoms, observed)
		var edits, matched, unexpected int
		i, j := 0, 0
		for _, e := range path {
			if e.i != i || e.j != j {
				t.Fatalf("iter %d: path node mismatch", iter)
			}
			switch e.op {
			case opMatch:
				matched++
				j += len(atoms[i].cells)
				i++
			case opDouble:
				edits += editDistance(atoms[i].cells, observed[j:j+2])
				j += 2
				i++
			case opSingle:
				edits += editDistance(atoms[i].cells, observed[j:j+1])
				j++
				i++
			case opMissing:
				edits += len(atoms[i].cells)
				i++
			case opUnexpected:
				edits++
				unexpected++
				j++
			}
		}
		if i != n || j != m {
			t.Fatalf("iter %d: path ends at (%d,%d), want (%d,%d)", iter, i, j, n, m)
		}
		if int(total.edits) != edits || int(total.matched) != matched || int(total.unexpected) != unexpected {
			t.Fatalf("iter %d: totals %+v != summed (%d,%d,%d)", iter, total, edits, matched, unexpected)
		}
	}
}

// TestProofreadMaxSizeStress exercises the grid at the readback limit so
// the int16 score fields and flat grid are exercised at scale.
func TestProofreadMaxSizeStress(t *testing.T) {
	text := strings.Repeat("a", MaxCodePoints) // 4000 expected cells
	observed := make([]int, MaxObservedCells)  // all zeros
	res, err := Proofread(text, ObservedFromInts(observed))
	if err != nil {
		t.Fatal(err)
	}
	if res.EditCount == 0 || res.Matched != 0 {
		t.Fatalf("unexpected alignment: %+v", res)
	}
}

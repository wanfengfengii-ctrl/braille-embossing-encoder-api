package braille

import "fmt"

// MaxObservedCells bounds the number of readback cells accepted by a
// proofread request.
const MaxObservedCells = 4000

// Discrepancy categories reported by Proofread. A perfect match is not a
// discrepancy and only increments the matched count.
const (
	// DiscrepancyMissing marks an atom that absorbed no readback cells:
	// its expected cells were not embossed (deletions).
	DiscrepancyMissing = "missing"
	// DiscrepancyUnexpected marks a readback cell no source atom accounts
	// for (stray embossing, an insertion).
	DiscrepancyUnexpected = "unexpected"
	// DiscrepancySingle marks an atom that absorbed exactly one readback
	// cell (a one-cell change).
	DiscrepancySingle = "single_change"
	// DiscrepancyDouble marks an atom that absorbed two readback cells
	// (a two-cell change).
	DiscrepancyDouble = "double_change"
)

// CodeInvalidObservedCells rejects a proofread request whose readback
// field is missing, too long, or contains an out-of-range cell value.
const CodeInvalidObservedCells = "invalid_observed_cells"

// Discrepancy is one divergence between the expected cell stream and the
// readback. Perfect alignments are not reported.
type Discrepancy struct {
	// Category is one of the Discrepancy* constants.
	Category string `json:"category"`
	// SourceIndex is the source character the atom belongs to. It is nil
	// for unexpected readback cells, which belong to no atom.
	SourceIndex *int `json:"source_index,omitempty"`
	// Expected holds the atom's expected cells (one or two); it is null
	// for unexpected readback cells, which have no expected counterpart.
	Expected []int `json:"expected"`
	// Observed holds the readback cells backing this discrepancy: none
	// for a missing atom, one for a single change or a stray cell, two for
	// a double change. It always serializes as an array, never null.
	Observed []int `json:"observed"`
	// ReadbackOffset is the offset of the first observed cell in the
	// readback stream; it is nil for a missing atom, which consumes no
	// readback cells.
	ReadbackOffset *int `json:"readback_offset,omitempty"`
}

// ProofreadResult is the alignment of one text against its readback.
type ProofreadResult struct {
	Matched       int           `json:"matched"`
	EditCount     int           `json:"edit_count"`
	Discrepancies []Discrepancy `json:"discrepancies"`
}

// atom is an indivisible alignment unit: the non-empty cell group of one
// non-newline source record. An atom always expects one cell (a space) or
// two (an indicator plus its body).
type atom struct {
	sourceIndex int
	cells       []int
}

// op identifies an alignment edge. The constants are ordered by the tie
// break preference — smallest first — so at the earliest divergence an
// optimal path prefers a perfect match, then a double change, then a
// single change, then a missing atom, then unexpected embossing.
type op uint8

const (
	opMatch op = iota
	opDouble
	opSingle
	opMissing
	opUnexpected
)

// score ranks an alignment suffix: minimize edits first, then maximize
// perfect matches, then minimize unexpected readback cells. Every field
// fits int16 — a path has at most 2*MaxCodePoints deletions plus
// MaxObservedCells insertions (8000 edits) and at most MaxCodePoints
// matches.
type score struct {
	edits      int16
	matched    int16
	unexpected int16
}

// better reports whether c is strictly better than s.
func (s score) better(c score) bool {
	if c.edits != s.edits {
		return c.edits < s.edits
	}
	if c.matched != s.matched {
		return c.matched > s.matched
	}
	return c.unexpected < s.unexpected
}

// Proofread reuses the encoder to derive the expected cell stream from
// text, skips newline records (which carry no cells), and aligns the
// readback observed cells against the expected atoms with a global dynamic
// program. Each record's cell group is an indivisible atom: it may stay a
// perfect match, absorb up to two consecutive readback cells, or absorb
// none (missing); readback cells no atom absorbs are unexpected embossing.
//
// The path first minimizes the total of insertions, deletions and
// substitutions, then maximizes perfect matches, then minimizes unexpected
// cells; among ties it chooses at the earliest divergence a perfect match,
// a double change, a single change, a missing atom, or unexpected embossing
// in that order.
//
// Text is validated first (the usual Encode errors); the observed cells
// are then validated as present, at most MaxObservedCells long, and each in
// [0, 63]. No partial alignment is ever returned with an error.
func Proofread(text string, observed ParsedObserved) (*ProofreadResult, *Error) {
	items, encErr := Encode(text)
	if encErr != nil {
		return nil, encErr
	}
	cells, err := ValidateObserved(observed)
	if err != nil {
		return nil, err
	}

	atoms := make([]atom, 0, len(items))
	for _, it := range items {
		if it.Kind == KindNewline {
			continue
		}
		atoms = append(atoms, atom{sourceIndex: it.SourceIndex, cells: it.Cells})
	}

	return align(atoms, cells), nil
}

// ParsedObserved is the type-checked readback array handed to the domain
// validator: the HTTP layer rejects any non-integer JSON element as a 400
// type error, so every entry here is a JSON integer literal. Overflow[i]
// marks an entry whose integer literal does not fit int64; it is an
// integer all the same and is therefore rejected as an out-of-range value
// (422), not a type error.
type ParsedObserved struct {
	Present  bool
	Integers []int64
	Overflow []bool
}

// ObservedFromInts builds a present ParsedObserved from plain ints, for
// callers (domain tests, verify client) that already hold in-memory cells.
func ObservedFromInts(cells []int) ParsedObserved {
	p := ParsedObserved{Present: cells != nil}
	if cells != nil {
		p.Integers = make([]int64, len(cells))
		p.Overflow = make([]bool, len(cells))
		for i, v := range cells {
			p.Integers[i] = int64(v)
		}
	}
	return p
}

// ValidateObserved enforces the readback contract in precedence order: a
// missing field, then an overlong stream, then the first out-of-range
// cell. Legal values lie in [0, 63].
func ValidateObserved(p ParsedObserved) ([]int, *Error) {
	if !p.Present {
		return nil, NewObservedCellsMissingError()
	}
	if len(p.Integers) > MaxObservedCells {
		return nil, NewObservedCellsTooLongError(len(p.Integers))
	}
	for i, v := range p.Integers {
		if p.Overflow[i] || v < 0 || v > 63 {
			var value *int
			if !p.Overflow[i] {
				x := int(v)
				value = &x
			}
			return nil, NewObservedCellValueError(i, value)
		}
	}
	cells := make([]int, len(p.Integers))
	for i, v := range p.Integers {
		cells[i] = int(v)
	}
	return cells, nil
}

// NewObservedCellsMissingError reports an absent observed_cells field.
func NewObservedCellsMissingError() *Error {
	return &Error{
		Code:    CodeInvalidObservedCells,
		Message: `request must include an "observed_cells" array of integers in 0-63`,
	}
}

// NewObservedCellsTooLongError reports a readback longer than the limit.
func NewObservedCellsTooLongError(length int) *Error {
	return &Error{
		Code:    CodeInvalidObservedCells,
		Message: fmt.Sprintf("observed_cells has %d cells, exceeding the limit of %d", length, MaxObservedCells),
		Length:  length,
		Limit:   MaxObservedCells,
	}
}

// NewObservedCellValueError reports the first observed cell outside 0-63;
// value is nil when the integer literal is too large to represent.
func NewObservedCellValueError(index int, value *int) *Error {
	e := &Error{
		Code:    CodeInvalidObservedCells,
		Message: fmt.Sprintf("observed_cells[%d] is outside the allowed range 0-63", index),
		Index:   &index,
	}
	if value != nil {
		e.Value = *value
		e.Message = fmt.Sprintf("observed_cells[%d] = %d is outside the allowed range 0-63", index, *value)
	}
	return e
}

// editDistance is the Levenshtein distance between two short cell slices
// (each at most two cells): the minimum insertions, deletions and
// substitutions turning want into got.
func editDistance(want, got []int) int {
	n, m := len(want), len(got)
	var d [3][3]int
	for i := 0; i <= n; i++ {
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			subst := 0
			if want[i-1] != got[j-1] {
				subst = 1
			}
			d[i][j] = min3(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+subst)
		}
	}
	return d[n][m]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// chosenEdge is one edge of the reconstructed path, recorded at its
// source node (i atoms and j readback cells consumed).
type chosenEdge struct {
	op op
	i  int
	j  int
}

// solveAlignment runs the global dynamic program over atoms (expected) and
// observed (readback) and returns the ordered path plus its score.
//
// best[i][j] holds the best score of the suffix from node (i,j) — i atoms
// and j readback cells consumed — to (n,m). Edges only move to larger
// (i,j), so the grid fills from (n,m) backwards. Reconstruction walks
// forward from (0,0): an edge is viable exactly when its local score plus
// the best suffix score of its target equals best[i][j], and edges are
// tried in tie-break order, which resolves the earliest divergence first.
func solveAlignment(atoms []atom, observed []int) ([]chosenEdge, score) {
	n, m := len(atoms), len(observed)
	width := m + 1

	best := make([]score, (n+1)*width)
	at := func(i, j int) *score { return &best[i*width+j] }

	// offer is the local cost of taking edge e at node (i,j), plus the
	// target's best suffix. ok=false means the edge is infeasible there.
	offer := func(i, j int, e op) (score, bool) {
		switch e {
		case opMatch:
			if i >= n {
				return score{}, false
			}
			cells := atoms[i].cells
			k := len(cells)
			if j+k > m {
				return score{}, false
			}
			for p, want := range cells {
				if observed[j+p] != want {
					return score{}, false
				}
			}
			s := *at(i+1, j+k)
			s.matched++
			return s, true
		case opDouble:
			if i >= n || j+2 > m {
				return score{}, false
			}
			s := *at(i+1, j+2)
			s.edits += int16(editDistance(atoms[i].cells, observed[j:j+2]))
			return s, true
		case opSingle:
			if i >= n || j+1 > m {
				return score{}, false
			}
			s := *at(i+1, j+1)
			s.edits += int16(editDistance(atoms[i].cells, observed[j:j+1]))
			return s, true
		case opMissing:
			if i >= n {
				return score{}, false
			}
			s := *at(i+1, j)
			s.edits += int16(len(atoms[i].cells))
			return s, true
		default: // opUnexpected
			if j+1 > m {
				return score{}, false
			}
			s := *at(i, j+1)
			s.edits++
			s.unexpected++
			return s, true
		}
	}

	for i := n; i >= 0; i-- {
		for j := m; j >= 0; j-- {
			if i == n && j == m {
				continue
			}
			cur := at(i, j)
			first := true
			for e := opMatch; e <= opUnexpected; e++ {
				cand, ok := offer(i, j, e)
				if !ok {
					continue
				}
				if first || cur.better(cand) {
					*cur = cand
					first = false
				}
			}
		}
	}

	// Greedy reconstruction: at each node take the first edge (in
	// tie-break order) that starts an optimal suffix.
	var path []chosenEdge
	i, j := 0, 0
	for i < n || j < m {
		var chosen op
		for e := opMatch; e <= opUnexpected; e++ {
			cand, ok := offer(i, j, e)
			if ok && cand == *at(i, j) {
				chosen = e
				break
			}
		}
		path = append(path, chosenEdge{op: chosen, i: i, j: j})
		switch chosen {
		case opMatch:
			j += len(atoms[i].cells)
			i++
		case opMissing:
			i++
		case opSingle:
			j++
			i++
		case opDouble:
			j += 2
			i++
		case opUnexpected:
			j++
		}
	}
	return path, best[0]
}

// align runs the global dynamic program and turns the chosen path into the
// ordered, non-perfect discrepancies.
func align(atoms []atom, observed []int) *ProofreadResult {
	path, total := solveAlignment(atoms, observed)
	result := &ProofreadResult{
		EditCount:     int(total.edits),
		Discrepancies: []Discrepancy{},
	}
	for _, ed := range path {
		i, j := ed.i, ed.j
		switch ed.op {
		case opMatch:
			result.Matched++
		case opMissing:
			a := atoms[i]
			idx := a.sourceIndex
			result.Discrepancies = append(result.Discrepancies, Discrepancy{
				Category:    DiscrepancyMissing,
				SourceIndex: &idx,
				Expected:    append([]int(nil), a.cells...),
				Observed:    []int{},
			})
		case opSingle:
			a := atoms[i]
			idx := a.sourceIndex
			off := j
			result.Discrepancies = append(result.Discrepancies, Discrepancy{
				Category:       DiscrepancySingle,
				SourceIndex:    &idx,
				Expected:       append([]int(nil), a.cells...),
				Observed:       []int{observed[j]},
				ReadbackOffset: &off,
			})
		case opDouble:
			a := atoms[i]
			idx := a.sourceIndex
			off := j
			result.Discrepancies = append(result.Discrepancies, Discrepancy{
				Category:       DiscrepancyDouble,
				SourceIndex:    &idx,
				Expected:       append([]int(nil), a.cells...),
				Observed:       []int{observed[j], observed[j+1]},
				ReadbackOffset: &off,
			})
		case opUnexpected:
			off := j
			result.Discrepancies = append(result.Discrepancies, Discrepancy{
				Category:       DiscrepancyUnexpected,
				Expected:       nil,
				Observed:       []int{observed[j]},
				ReadbackOffset: &off,
			})
		}
	}
	return result
}

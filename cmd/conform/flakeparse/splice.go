package flakeparse

// Splice is one pending byte-range edit:
//   - Insert: End == 0 (or End == Offset), Text non-empty → insert Text at Offset
//   - Delete: End > Offset, Text == ""  → remove bytes [Offset, End)
//   - Replace: End > Offset, Text non-empty → replace [Offset, End) with Text
//
// Multiple splices applied to the same source must target non-overlapping
// ranges. Sort by descending Offset before applying so earlier offsets
// remain valid after each application. Two splices at the same Offset
// have undefined relative order under sort.Slice (it is not stable); if
// that arises, use sort.SliceStable with a secondary comparator.
type Splice struct {
	Offset int
	End    int
	Text   string
}

// ApplyTo returns src with the splice applied.
func (s Splice) ApplyTo(src []byte) []byte {
	if s.End > s.Offset {
		out := make([]byte, 0, len(src)-(s.End-s.Offset)+len(s.Text))
		out = append(out, src[:s.Offset]...)
		out = append(out, s.Text...)
		out = append(out, src[s.End:]...)

		return out
	}

	return spliceAt(src, s.Offset, s.Text)
}

// spliceAt inserts ins at byte offset off in src.
func spliceAt(src []byte, off int, ins string) []byte {
	out := make([]byte, 0, len(src)+len(ins))
	out = append(out, src[:off]...)
	out = append(out, ins...)
	out = append(out, src[off:]...)

	return out
}

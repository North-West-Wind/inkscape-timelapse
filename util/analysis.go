package util

import "io"

type resChange struct {
	fromW, fromH int
	toW, toH     int
	count        int
}

type Analysis struct {
	count, emptyCount int
	initW, initH      int
	maxW, maxH        int
	minW, minH        int
	resChanges        []resChange
}

func (tr *TLDReader) Analyze() Analysis {
	var a Analysis
	var svg string
	var rec Record
	var prevW, prevH int

	a.resChanges = make([]resChange, 0)

	for {
		err := tr.ReadRecord(&rec)
		if err != nil {
			break
		}
		if isNonEmpty(rec.Diff) {
			svg = ApplyDiff(svg, rec.Diff)
		}
		if !isNonEmpty(rec.Diff) {
			a.emptyCount++
		}
		if a.count == 0 {
			w, h := svgSize(svg)
			if w > 0 && h > 0 {
				a.initW, a.initH = w, h
				a.maxW, a.maxH = w, h
				a.minW, a.minH = w, h
			}
		} else {
			w, h := svgSize(svg)
			if w != prevW || h != prevH {
				a.resChanges = append(a.resChanges, resChange{
					fromW: prevW,
					fromH: prevH,
					toW:   w,
					toH:   h,
					count: a.count,
				})
				prevW = w
				prevH = h
			}
			if w > 0 && h > 0 {
				if w > a.maxW {
					a.maxW = w
				}
				if h > a.maxH {
					a.maxH = h
				}
				if w < a.minW {
					a.minW = w
				}
				if h < a.minH {
					a.minH = h
				}
			}
		}
		a.count++
	}

	// Be kind, rewind
	tr.f.Seek(4, io.SeekStart)
	return a
}

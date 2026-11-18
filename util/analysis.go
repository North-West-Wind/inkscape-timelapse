package util

import (
	"io"
	"strings"
)

type resChange struct {
	FromW, FromH int
	ToW, ToH     int
	Count        int
}

type Analysis struct {
	Count, EmptyCount int
	InitW, InitH      int
	MaxW, MaxH        int
	MinW, MinH        int
	ResChanges        []resChange
}

func (tr *SVGDReader) Analyze() Analysis {
	var a Analysis
	var svg string
	var rec Record
	var prevW, prevH int

	a.ResChanges = make([]resChange, 0)

	for {
		err := tr.ReadRecord(&rec)
		if err != nil {
			break
		}
		if strings.TrimSpace(rec.Diff) != "" {
			svg = ApplyDiff(svg, rec.Diff)
		} else {
			a.EmptyCount++
		}
		if a.Count == 0 {
			w, h := svgSize(svg)
			if w > 0 && h > 0 {
				a.InitW, a.InitH = w, h
				a.MaxW, a.MaxH = w, h
				a.MinW, a.MinH = w, h
			}
		} else {
			w, h := svgSize(svg)
			if w != prevW || h != prevH {
				a.ResChanges = append(a.ResChanges, resChange{
					FromW: prevW,
					FromH: prevH,
					ToW:   w,
					ToH:   h,
					Count: a.Count,
				})
				prevW = w
				prevH = h
			}
			if w > 0 && h > 0 {
				if w > a.MaxW {
					a.MaxW = w
				}
				if h > a.MaxH {
					a.MaxH = h
				}
				if w < a.MinW {
					a.MinW = w
				}
				if h < a.MinH {
					a.MinH = h
				}
			}
		}
		a.Count++
	}

	// Be kind, rewind
	tr.f.Seek(4, io.SeekStart)
	return a
}

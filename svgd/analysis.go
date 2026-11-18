package svgd

import (
	"io"
)

type ResolutionChange struct {
	FromWidth, FromHeight int
	ToWidth, ToHeight     int
	Frame                 int
}

type Analysis struct {
	Frames, EmptyFrames         int
	InitialWidth, InitialHeight int
	MaxWidth, MaxHeight         int
	MinWidth, MinHeight         int
	ResolutionChanges           []ResolutionChange
}

func (tr *SVGDReader) Analyze() Analysis {
	var a Analysis
	var svg string
	var rec Record
	var prevW, prevH int

	a.ResolutionChanges = make([]ResolutionChange, 0)

	for {
		err := tr.ReadRecord(&rec)
		if err != nil {
			break
		}
		if IsNonEmpty(rec.Diff) {
			svg = Patch(svg, rec.Diff)
		} else {
			a.EmptyFrames++
		}
		if a.Frames == 0 {
			w, h := svgSize(svg)
			if w > 0 && h > 0 {
				a.InitialWidth, a.InitialHeight = w, h
				a.MaxWidth, a.MaxHeight = w, h
				a.MinWidth, a.MinHeight = w, h
			}
		} else {
			w, h := svgSize(svg)
			if w != prevW || h != prevH {
				a.ResolutionChanges = append(a.ResolutionChanges, ResolutionChange{
					FromWidth:  prevW,
					FromHeight: prevH,
					ToWidth:    w,
					ToHeight:   h,
					Frame:      a.Frames,
				})
				prevW = w
				prevH = h
			}
			if w > 0 && h > 0 {
				if w > a.MaxWidth {
					a.MaxWidth = w
				}
				if h > a.MaxHeight {
					a.MaxHeight = h
				}
				if w < a.MinWidth {
					a.MinWidth = w
				}
				if h < a.MinHeight {
					a.MinHeight = h
				}
			}
		}
		a.Frames++
	}

	// Be kind, rewind
	tr.f.Seek(4, io.SeekStart)
	return a
}

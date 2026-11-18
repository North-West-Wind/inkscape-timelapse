package svgd

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type SVGDReader struct {
	file *os.File
}

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

func OpenSVGDRead(path string) (*SVGDReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 4)
	n, err := file.Read(header)
	if err != nil {
		return nil, err
	}
	if n < 4 || string(header) != Magic && string(header) != OldMagic {
		return nil, fmt.Errorf("invalid .svgd magic")
	}
	return &SVGDReader{file: file}, nil
}

func (tr *SVGDReader) ReadRecord(record *Record) error {
	recLenB := make([]byte, 4)
	n, err := tr.file.Read(recLenB)
	if err != nil {
		return err
	}
	if n < 4 {
		return fmt.Errorf("invalid record length")
	}
	recLen := binary.LittleEndian.Uint32(recLenB)
	data := make([]byte, recLen)
	n, err = tr.file.Read(data)
	if err != nil {
		return err
	}
	if n < int(recLen) {
		return fmt.Errorf("invalid record data")
	}
	rc := flate.NewReader(bytes.NewReader(data))
	raw, err := io.ReadAll(rc)
	rc.Close()
	if err != nil && len(raw) == 0 {
		return fmt.Errorf("invalid record data")
	}
	if len(raw) < 12 {
		return fmt.Errorf("invalid record data")
	}
	ts := binary.LittleEndian.Uint64(raw[0:8])
	dlen := int(binary.LittleEndian.Uint32(raw[8:12]))
	if 12+dlen > len(raw) {
		return fmt.Errorf("invalid record data")
	}
	diff := string(raw[12 : 12+dlen])
	*record = Record{Timestamp: ts, Diff: diff, Length: recLen}
	return nil
}

func (tr *SVGDReader) Close() error {
	return tr.file.Close()
}

func (tr *SVGDReader) Analyze() Analysis {
	var analysis Analysis
	var svg string
	var rec Record
	var prevW, prevH int

	analysis.ResolutionChanges = make([]ResolutionChange, 0)

	for {
		err := tr.ReadRecord(&rec)
		if err != nil {
			break
		}
		if IsNonEmpty(rec.Diff) {
			svg = Patch(svg, rec.Diff)
		} else {
			analysis.EmptyFrames++
		}
		if analysis.Frames == 0 {
			w, h := svgSize(svg)
			if w > 0 && h > 0 {
				analysis.InitialWidth, analysis.InitialHeight = w, h
				analysis.MaxWidth, analysis.MaxHeight = w, h
				analysis.MinWidth, analysis.MinHeight = w, h
			}
		} else {
			w, h := svgSize(svg)
			if w != prevW || h != prevH {
				analysis.ResolutionChanges = append(analysis.ResolutionChanges, ResolutionChange{
					FromWidth:  prevW,
					FromHeight: prevH,
					ToWidth:    w,
					ToHeight:   h,
					Frame:      analysis.Frames,
				})
				prevW = w
				prevH = h
			}
			if w > 0 && h > 0 {
				if w > analysis.MaxWidth {
					analysis.MaxWidth = w
				}
				if h > analysis.MaxHeight {
					analysis.MaxHeight = h
				}
				if w < analysis.MinWidth {
					analysis.MinWidth = w
				}
				if h < analysis.MinHeight {
					analysis.MinHeight = h
				}
			}
		}
		analysis.Frames++
	}

	// Be kind, rewind
	tr.file.Seek(4, io.SeekStart)
	return analysis
}

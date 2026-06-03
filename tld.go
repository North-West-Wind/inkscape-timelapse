package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

const Magic = "TLTL"

type Record struct {
	TS   uint64
	Diff string
}

// ── Writer (length-prefixed per-record deflate, crash-safe) ───────

type TLDWriter struct {
	mu        sync.Mutex
	f         *os.File
	syncCount int
}

func CreateTLD(path string) (*TLDWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(f, Magic); err != nil {
		f.Close()
		return nil, err
	}
	return &TLDWriter{f: f}, nil
}

func OpenTLDAppend(path string) (*TLDWriter, string, int, int, error) {
	lastSVG, count, bytes, err := ReconstructSVG(path)
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("reconstruct %s: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, "", 0, 0, err
	}
	return &TLDWriter{f: f}, lastSVG, count, bytes, nil
}

func (tw *TLDWriter) WriteCapture(ts uint64, diff string) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	diffB := []byte(diff)
	payload := make([]byte, 8+4+len(diffB))
	binary.LittleEndian.PutUint64(payload[0:8], ts)
	binary.LittleEndian.PutUint32(payload[8:12], uint32(len(diffB)))
	copy(payload[12:], diffB)

	buf := &bytes.Buffer{}
	w, err := flate.NewWriter(buf, flate.DefaultCompression)
	if err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	compressed := buf.Bytes()
	lenLE := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenLE, uint32(len(compressed)))
	if _, err := tw.f.Write(lenLE); err != nil {
		return err
	}
	if _, err := tw.f.Write(compressed); err != nil {
		return err
	}

	tw.syncCount++
	if tw.syncCount >= 10 {
		tw.syncCount = 0
		return tw.f.Sync()
	}
	return nil
}

func (tw *TLDWriter) Close() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.f.Close()
}

// ── Reader ────────────────────────────────────────────────────────

type TLDReader struct {
	f *os.File
}

func OpenTLDRead(path string) (*TLDReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 4)
	n, err := f.Read(header)
	if err != nil {
		return nil, err
	}
	if n < 4 || string(header) != Magic {
		return nil, fmt.Errorf("invalid .tld magic")
	}
	return &TLDReader{f: f}, nil
}

func (tr *TLDReader) ReadRecord(record *Record) error {
	recLenB := make([]byte, 4)
	n, err := tr.f.Read(recLenB)
	if err != nil {
		return err
	}
	if n < 4 {
		return fmt.Errorf("invalid record length")
	}
	recLen := binary.LittleEndian.Uint32(recLenB)
	data := make([]byte, recLen)
	n, err = tr.f.Read(data)
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
	*record = Record{TS: ts, Diff: diff}
	return nil
}

// ── Analyze ───────────────────────────────────────────────────────

type Analysis struct {
	count, emptyCount int
	initW, initH      int
	maxW, maxH        int
	minW, minH        int
}

func (tr *TLDReader) Analyze() Analysis {
	var a Analysis
	var svg string
	var rec Record

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

func (tr *TLDReader) Close() error {
	return tr.f.Close()
}

// ── Reconstruction (for --append) ─────────────────────────────────

func ReconstructSVG(path string) (lastSVG string, captures int, totalBytes int, err error) {
	tr, err := OpenTLDRead(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer tr.Close()

	var svg string
	var rec Record
	for {
		err = tr.ReadRecord(&rec)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "Error reading %s at frame %d: %v\n", path, captures, err)
			}
			break
		}

		captures++
		totalBytes += len(rec.Diff)
		if isNonEmpty(rec.Diff) {
			svg = ApplyDiff(svg, rec.Diff)
		}
	}
	return svg, captures, totalBytes, nil
}

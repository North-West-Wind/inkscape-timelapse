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

const (
	Magic    = "TLTL"
	RInitial = 0
	RCapture = 1
)

type Record struct {
	Type int
	SVG  string
	TS   string
	Diff string
}

// ── Writer (length-prefixed per-record deflate, crash-safe) ───────

type TLDWriter struct {
	mu        sync.Mutex
	f         *os.File
	syncCount int // periodic fsync every N writes
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

func OpenTLDAppend(path string) (*TLDWriter, string, int, error) {
	lastSVG, count, err := ReconstructSVG(path)
	if err != nil {
		return nil, "", 0, fmt.Errorf("reconstruct %s: %w", path, err)
	}
	if lastSVG == "" {
		return nil, "", 0, fmt.Errorf("empty recording: %s", path)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, "", 0, err
	}
	return &TLDWriter{f: f}, lastSVG, count, nil
}

func (tw *TLDWriter) WriteInitial(svg string) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	svgB := []byte(svg)
	payload := make([]byte, 4+len(svgB))
	binary.LittleEndian.PutUint32(payload[0:4], uint32(len(svgB)))
	copy(payload[4:], svgB)
	return tw.writeCompressed(RInitial, payload)
}

func (tw *TLDWriter) WriteCapture(ts, diff string) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tsB := []byte(ts)
	diffB := []byte(diff)
	payload := make([]byte, 4+len(tsB)+4+len(diffB))
	binary.LittleEndian.PutUint32(payload[0:4], uint32(len(tsB)))
	copy(payload[4:], tsB)
	binary.LittleEndian.PutUint32(payload[4+len(tsB):4+len(tsB)+4], uint32(len(diffB)))
	copy(payload[4+len(tsB)+4:], diffB)
	return tw.writeCompressed(RCapture, payload)
}

func (tw *TLDWriter) writeCompressed(typ int, payload []byte) error {
	raw := make([]byte, 1+len(payload))
	raw[0] = byte(typ)
	copy(raw[1:], payload)

	buf := &bytes.Buffer{}
	w, err := flate.NewWriter(buf, flate.DefaultCompression)
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
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

func ReadRecords(path string) ([]Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 4 || string(data[:4]) != Magic {
		return nil, fmt.Errorf("invalid .tld magic")
	}
	return readLengthPrefixed(data[4:])
}

func readLengthPrefixed(body []byte) ([]Record, error) {
	offset := 0
	var records []Record

	for offset+4 <= len(body) {
		recLen := int(binary.LittleEndian.Uint32(body[offset : offset+4]))
		if recLen <= 0 || recLen > 10*1024*1024 {
			break
		}
		offset += 4
		if offset+recLen > len(body) {
			break
		}
		rc := flate.NewReader(bytes.NewReader(body[offset : offset+recLen]))
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil && len(raw) == 0 {
			break
		}
		if len(raw) == 0 {
			break
		}
		records = append(records, parseRaw(raw)...)
		offset += recLen
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("no records found")
	}
	return records, nil
}

func parseRaw(raw []byte) []Record {
	var records []Record
	i := 0
	for i < len(raw) {
		if i+5 > len(raw) {
			break
		}
		t := int(raw[i])
		if t == RInitial {
			slen := int(binary.LittleEndian.Uint32(raw[i+1 : i+5]))
			end := i + 5 + slen
			if end > len(raw) {
				break
			}
			records = append(records, Record{Type: RInitial, SVG: string(raw[i+5 : end])})
			i = end
		} else if t == RCapture {
			tslen := int(binary.LittleEndian.Uint32(raw[i+1 : i+5]))
			if i+5+tslen+4 > len(raw) {
				break
			}
			ts := string(raw[i+5 : i+5+tslen])
			dlen := int(binary.LittleEndian.Uint32(raw[i+5+tslen : i+5+tslen+4]))
			end := i + 5 + tslen + 4 + dlen
			if end > len(raw) {
				break
			}
			diff := string(raw[i+5+tslen+4 : end])
			records = append(records, Record{Type: RCapture, TS: ts, Diff: diff})
			i = end
		} else {
			break
		}
	}
	return records
}

// ── Reconstruction (for --append) ─────────────────────────────────

func ReconstructSVG(path string) (lastSVG string, captures int, err error) {
	records, err := ReadRecords(path)
	if err != nil {
		return "", 0, err
	}
	var svg string
	for _, rec := range records {
		if rec.Type == RInitial {
			svg = rec.SVG
		} else {
			captures++
			if isNonEmpty(rec.Diff) {
				svg = ApplyDiff(svg, rec.Diff)
			}
		}
	}
	return svg, captures, nil
}

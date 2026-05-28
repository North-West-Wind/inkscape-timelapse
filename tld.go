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

func ReadRecords(path string) ([]Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 4 || string(data[:4]) != Magic {
		return nil, fmt.Errorf("invalid .tld magic")
	}
	return readRecords(data[4:])
}

func readRecords(body []byte) ([]Record, error) {
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
		if len(raw) < 12 {
			break
		}
		ts := binary.LittleEndian.Uint64(raw[0:8])
		dlen := int(binary.LittleEndian.Uint32(raw[8:12]))
		if 12+dlen > len(raw) {
			break
		}
		diff := string(raw[12 : 12+dlen])
		records = append(records, Record{TS: ts, Diff: diff})
		offset += recLen
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("no records found")
	}
	return records, nil
}

// ── Reconstruction (for --append) ─────────────────────────────────

func ReconstructSVG(path string) (lastSVG string, captures int, totalBytes int, err error) {
	records, err := ReadRecords(path)
	if err != nil {
		return "", 0, 0, err
	}
	var svg string
	for _, rec := range records {
		captures++
		totalBytes += len(rec.Diff)
		if isNonEmpty(rec.Diff) {
			svg = ApplyDiff(svg, rec.Diff)
		}
	}
	return svg, captures, totalBytes, nil
}

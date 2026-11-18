package util

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
	OldMagic = "TLTL"
	Magic    = "IkOd"
)

type Record struct {
	Timestamp uint64
	Diff      string
}

type SVGDWriter struct {
	mu        sync.Mutex
	f         *os.File
	syncCount int
}

func CreateSVGD(path string) (*SVGDWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(f, Magic); err != nil {
		f.Close()
		return nil, err
	}
	return &SVGDWriter{f: f}, nil
}

func OpenSVGDAppend(path string) (*SVGDWriter, string, int, int, error) {
	lastSVG, count, bytes, err := ReconstructSVG(path)
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("reconstruct %s: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, "", 0, 0, err
	}
	return &SVGDWriter{f: f}, lastSVG, count, bytes, nil
}

func (tw *SVGDWriter) WriteCapture(ts uint64, diff string) error {
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

func (tw *SVGDWriter) Close() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.f.Close()
}

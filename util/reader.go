package util

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type SVGDReader struct {
	f *os.File
}

func OpenSVGDRead(path string) (*SVGDReader, error) {
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
	return &SVGDReader{f: f}, nil
}

func (tr *SVGDReader) ReadRecord(record *Record) error {
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
	*record = Record{Timestamp: ts, Diff: diff}
	return nil
}

func (tr *SVGDReader) Close() error {
	return tr.f.Close()
}

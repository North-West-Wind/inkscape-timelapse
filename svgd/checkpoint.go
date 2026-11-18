package svgd

import "io"

const checkpointInterval = 100

type Checkpoint struct {
	svg          string
	position     int
	bytePosition int64
}

type CheckpointReader struct {
	reader      *SVGDReader
	record      Record
	svg         string
	position    int
	checkpoints []Checkpoint
}

func NewCheckpointReader(reader *SVGDReader) *CheckpointReader {
	var cr CheckpointReader
	cr.reader = reader
	cr.svg = ""
	cr.position = 0
	cr.checkpoints = make([]Checkpoint, 0)
	return &cr
}

func (cr *CheckpointReader) ReadSVG(frame int) (string, string, error) {
	var checkpoint *Checkpoint
	for _, cp := range cr.checkpoints {
		if frame < cp.position {
			break
		}
		checkpoint = &cp
	}

	if checkpoint == nil {
		checkpoint = &Checkpoint{
			svg:          "",
			position:     0,
			bytePosition: 0,
		}
	}

	// Frame is behind current position, rewind
	if frame < cr.position {
		cr.svg = checkpoint.svg
		cr.position = checkpoint.position
		cr.reader.file.Seek(checkpoint.bytePosition, io.SeekStart)
	}

	for cr.position <= frame {
		err := cr.reader.ReadRecord(&cr.record)
		if err != nil {
			return cr.svg, cr.record.Diff, err
		}

		// Save checkpoint sometimes
		if cr.position%checkpointInterval == 0 && len(cr.checkpoints) <= cr.position/checkpointInterval {
			bytePosition, _ := cr.reader.file.Seek(0, io.SeekCurrent)
			bytePosition -= 4 + int64(cr.record.Length)
			cr.checkpoints = append(cr.checkpoints, Checkpoint{
				svg:          cr.svg,
				position:     cr.position,
				bytePosition: bytePosition,
			})
		}
		cr.svg = Patch(cr.svg, cr.record.Diff)
		cr.position++
	}
	return cr.svg, cr.record.Diff, nil
}

func (cr *CheckpointReader) Position() int {
	return cr.position
}

package tailer

import (
	"bytes"
	"errors"
	"time"
)

// criRecord is one parsed CRI log record.
type criRecord struct {
	Time    time.Time
	Stream  string
	Partial bool
	Text    []byte
}

var errBadCRI = errors.New("bad CRI line")

// parseCRI reads one CRI record from a raw log line without the newline.
// The format is "<time> <stream> <P|F> <content>".
func parseCRI(line []byte) (criRecord, error) {
	// Split into at most four fields. The content can hold spaces.
	before, after, ok := bytes.Cut(line, []byte{' '})
	if !ok {
		return criRecord{}, errBadCRI
	}
	rest := after
	second := bytes.IndexByte(rest, ' ')
	if second < 0 {
		return criRecord{}, errBadCRI
	}
	stream := rest[:second]
	rest = rest[second+1:]
	third := bytes.IndexByte(rest, ' ')
	if third < 0 {
		return criRecord{}, errBadCRI
	}
	flag := rest[:third]
	content := rest[third+1:]

	t, err := time.Parse(time.RFC3339Nano, string(before))
	if err != nil {
		return criRecord{}, errBadCRI
	}
	var partial bool
	switch string(flag) {
	case "P":
		partial = true
	case "F":
		partial = false
	default:
		return criRecord{}, errBadCRI
	}
	return criRecord{Time: t, Stream: string(stream), Partial: partial, Text: content}, nil
}

// criJoiner joins partial CRI records into one line. It keeps the time and the
// stream of the first record in the group.
type criJoiner struct {
	buf    []byte
	time   time.Time
	stream string
	open   bool
}

// add takes one record. It returns the joined record and true when the line is
// complete. It returns false while the line is still partial.
func (j *criJoiner) add(rec criRecord) (criRecord, bool) {
	if !j.open {
		j.time = rec.Time
		j.stream = rec.Stream
		j.buf = j.buf[:0]
	}
	j.buf = append(j.buf, rec.Text...)
	if rec.Partial {
		j.open = true
		return criRecord{}, false
	}
	j.open = false
	out := criRecord{Time: j.time, Stream: j.stream, Text: append([]byte(nil), j.buf...)}
	return out, true
}
